// Package comment is the `/tofu plan|apply|unlock` comment parser of comment-ops.yml and its "nothing matched" reply.
// It is the privilege boundary of `/tofu apply`: who may run it, on which pull request, with which arguments. It fails closed,
// matches strictly and exactly, never uses a shell, and counts text in characters.
package comment

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/ghactions"
)

// Env is everything the package touches outside the process.
type Env struct {
	Getenv func(string) string
	Stdout io.Writer
	Stderr io.Writer
	GH     gh.Client
}

var (
	repoRE   = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	actorRE  = regexp.MustCompile(`^[A-Za-z0-9_.\[\]-]+$`)
	numberRE = regexp.MustCompile(`^[0-9]+$`)
	shaRE    = regexp.MustCompile(`^[0-9a-f]{6,64}$`)
	nameRE   = regexp.MustCompile(`^[A-Za-z0-9_./*?-]+$`)
	// user text goes into a markdown comment: backticks are dropped, < and > escaped (GitHub would hide <tags>)
	escapeText = strings.NewReplacer("<", "&lt;", ">", "&gt;")
)

type parser struct {
	e       Env
	r       *ghactions.Runner
	repo    string
	actor   string
	pr      string
	comment string
	body    string
}

// fail tells the user why a command was rejected (a reply on the pull request and a confused reaction) and fails the step.
func (p *parser) fail(msg string) int {
	p.r.Error(msg)
	first, _, _ := strings.Cut(strings.ReplaceAll(p.body, "`", ""), "\n")
	shown := []rune(first)
	if len(shown) > 120 {
		shown = shown[:120]
	}
	clean := escapeText.Replace(strings.ReplaceAll(msg, "`", ""))
	p.e.GH.Run("pr", "comment", p.pr, "--repo", p.repo, "--body", "❌ `"+string(shown)+"`: "+clean)
	p.react("confused")
	return 1
}

func (p *parser) react(content string) {
	p.e.GH.Run("api", "-X", "POST", "repos/"+p.repo+"/issues/comments/"+p.comment+"/reactions", "-f", "content="+content)
}

// splitBlank splits on spaces and tabs only (the first line has no other line break).
func splitBlank(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\t' })
}

// pathEscape escapes each segment of a ref for a URL path and keeps the slashes.
func pathEscape(ref string) string {
	parts := strings.Split(ref, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

type pull struct {
	State string `json:"state"`
	Head  struct {
		SHA  string `json:"sha"`
		Repo *struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
}

// Parse reads the event from BODY, ACTOR, PR, COMMENT_ID, REQUIRE_UTD, GITHUB_REPOSITORY and writes the outputs
// cmd, now, targets, sha, names, base, base_ref.
func Parse(e Env) int {
	g := e.Getenv
	p := &parser{e: e, r: &ghactions.Runner{Getenv: g, Stdout: e.Stdout}, repo: g("GITHUB_REPOSITORY"), actor: g("ACTOR"),
		pr: g("PR"), comment: g("COMMENT_ID"), body: g("BODY")}
	die := func(f string, a ...any) int { fmt.Fprintf(e.Stderr, "tfpls comment: "+f+"\n", a...); return 1 }
	// the values go into API paths: anything unexpected stops the run before a request is made
	if !repoRE.MatchString(p.repo) || !actorRE.MatchString(p.actor) || !numberRE.MatchString(p.pr) || !numberRE.MatchString(p.comment) {
		return die("GITHUB_REPOSITORY, ACTOR, PR and COMMENT_ID must be set and well formed")
	}
	utd := g("REQUIRE_UTD")
	if utd != "true" && utd != "false" {
		return die("REQUIRE_UTD must be true or false")
	}

	// who: the permission is the one GitHub reports for the commenter now
	out, err := e.GH.Run("api", "repos/"+p.repo+"/collaborators/"+p.actor+"/permission")
	if err != nil {
		return die("%v", err)
	}
	var pm struct {
		Permission string `json:"permission"`
	}
	if json.Unmarshal([]byte(out), &pm) != nil {
		return die("the permission answer is not JSON")
	}
	perm := pm.Permission
	if perm != "admin" && perm != "write" && perm != "maintain" {
		return p.fail(p.actor + " lacks write access")
	}
	p.react("eyes")

	// the pull request: open and from this repository (fork code never gets credentials)
	out, err = e.GH.Run("api", "repos/"+p.repo+"/pulls/"+p.pr)
	if err != nil {
		return die("%v", err)
	}
	var pr pull
	if json.Unmarshal([]byte(out), &pr) != nil {
		return die("the pull request answer is not JSON")
	}
	if pr.State != "open" {
		return p.fail("this pull request is not open")
	}
	if pr.Head.Repo == nil || pr.Head.Repo.FullName != p.repo {
		return p.fail("fork PRs are not supported")
	}
	sha, base := pr.Head.SHA, pr.Base.Ref
	if !shaRE.MatchString(sha) || !shaRE.MatchString(pr.Base.SHA) || base == "" {
		return die("the pull request answer has no usable head or base")
	}

	// the command: the first line, exactly "/tofu <plan|apply|unlock> ..."
	first, _, _ := strings.Cut(p.body, "\n")
	words := splitBlank(strings.ReplaceAll(first, "\r", ""))
	slash, cmd, rest := "", "", []string(nil)
	if len(words) > 0 {
		slash, words = words[0], words[1:]
	}
	if len(words) > 0 {
		cmd, rest = words[0], words[1:]
	}
	if slash != "/tofu" || (cmd != "plan" && cmd != "apply" && cmd != "unlock") {
		return p.fail("unknown command: " + cmd)
	}
	var names, targets []string
	now := false
	for _, tok := range rest {
		switch {
		case tok == "--now":
			now = true
		case strings.HasPrefix(tok, "-target="):
			t := strings.TrimPrefix(tok, "-target=")
			if t == "" {
				return p.fail("unexpected argument: " + tok)
			}
			targets = append(targets, t)
		case nameRE.MatchString(tok) && !strings.HasPrefix(tok, "-"):
			names = append(names, tok)
		default:
			return p.fail("unexpected argument: " + tok)
		}
	}
	nameList := strings.Join(names, " ")
	oneName := len(names) == 1 && !strings.Contains(nameList, "*") && nameList != "all"

	// --now: a repository admin applies without the approvals the repository's rules ask for. Everything else still holds.
	if now {
		if cmd != "apply" {
			return p.fail("--now only goes with /tofu apply")
		}
		if perm != "admin" {
			return p.fail("only repository admins can use --now (you have " + perm + " access)")
		}
		e.GH.Run("pr", "comment", p.pr, "--repo", p.repo, "--body", "⚡ **`--now`** used by @"+p.actor+": the approvals the repository asks for are skipped for this apply. The reviewed plan, the policies and the up-to-date check still apply. Someone should review this afterwards.")
	}
	if len(targets) > 0 && !oneName {
		return p.fail("-target requires exactly one workspace (no globs/all)")
	}
	if cmd == "unlock" && (len(targets) > 0 || !oneName || strings.Contains(nameList, "?")) {
		return p.fail("usage: /tofu unlock <workspace> (exactly one, no globs)")
	}
	if cmd == "apply" && len(names) == 0 {
		return p.fail("apply needs explicit workspace(s): /tofu apply <workspace…|glob|all>")
	}
	if cmd == "apply" && utd == "true" {
		out, err := e.GH.Run("api", "repos/"+p.repo+"/compare/"+pathEscape(base)+"..."+sha)
		if err != nil {
			return die("%v", err)
		}
		var cmp struct {
			Behind *int `json:"behind_by"`
		}
		if json.Unmarshal([]byte(out), &cmp) != nil || cmp.Behind == nil {
			return die("the compare answer has no behind_by")
		}
		if *cmp.Behind != 0 {
			return p.fail(fmt.Sprintf("branch is %d commits behind %s; update it first", *cmp.Behind, base))
		}
	}
	for _, kv := range [][2]string{{"cmd", cmd}, {"now", fmt.Sprint(now)}, {"targets", strings.Join(targets, " ")}, {"sha", sha},
		{"names", nameList}, {"base", pr.Base.SHA}, {"base_ref", base}} {
		if err := p.r.SetOutput(kv[0], kv[1]); err != nil {
			return die("%v", err)
		}
	}
	return 0
}

// NoMatch answers a command whose names matched no workspace. NAMES and CMD come from Parse's outputs and are checked again.
func NoMatch(e Env) int {
	g := e.Getenv
	repo, pr, id, names, cmd := g("GITHUB_REPOSITORY"), g("PR"), g("COMMENT_ID"), g("NAMES"), g("CMD")
	if !repoRE.MatchString(repo) || !numberRE.MatchString(pr) || !numberRE.MatchString(id) ||
		(cmd != "plan" && cmd != "apply" && cmd != "unlock") || (names != "" && !validNames(names)) {
		fmt.Fprintln(e.Stderr, "tfpls comment nomatch: GITHUB_REPOSITORY, PR, COMMENT_ID, CMD or NAMES is missing or malformed")
		return 1
	}
	var msg string
	if names != "" {
		msg = "No workspace matches `" + names + "`. Check the spelling; workspaces are the names in the PR plan comments and in the config file."
	} else {
		msg = "This pull request changes no workspace, so there is nothing to " + cmd + ". Name a workspace, for example `/tofu " + cmd + " <workspace>`."
	}
	e.GH.Run("pr", "comment", pr, "--repo", repo, "--body", "ℹ️ "+msg)
	e.GH.Run("api", "-X", "POST", "repos/"+repo+"/issues/comments/"+id+"/reactions", "-f", "content=confused")
	return 0
}

func validNames(s string) bool {
	for _, n := range splitBlank(s) {
		if !nameRE.MatchString(n) || strings.HasPrefix(n, "-") {
			return false
		}
	}
	return true
}

// Main dispatches `tfpls comment parse|nomatch`.
func Main(args []string, e Env) int {
	if len(args) == 1 && args[0] == "parse" {
		return Parse(e)
	}
	if len(args) == 1 && args[0] == "nomatch" {
		return NoMatch(e)
	}
	fmt.Fprintln(e.Stderr, "usage: tfpls comment parse|nomatch")
	return 2
}
