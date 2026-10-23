// Package driftfix parses and guards a drift fix (a `/tofu plan|apply|fix [-target=addr ...]` comment on an open `Drift: <workspace>`
// issue made by the pipeline, or a manual run) and drops the workspaces that forbid it (`drift_fix: false`).
// A refusal is explained on the issue; a problem with the person or the arguments fails the job, an unrelated issue is ignored.
package driftfix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
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
	titleRE   = regexp.MustCompile(`^Drift: ([A-Za-z0-9_./-]+)$`)
	targetRE  = regexp.MustCompile(`^[\]\[A-Za-z0-9_."'-]+$`)
	nameRE    = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)
	actorRE   = regexp.MustCompile(`^[A-Za-z0-9_.\[\]-]+$`)
	branchRE  = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	numberRE  = regexp.MustCompile(`^[0-9]+$`)
	cleanText = strings.NewReplacer("<", "&lt;", ">", "&gt;")
)

type parser struct {
	e     Env
	r     *ghactions.Runner
	repo  string
	issue string
}

func clean(m string) string { return cleanText.Replace(strings.ReplaceAll(m, "`", "")) }

func (p *parser) reply(body string) {
	if p.issue != "" && numberRE.MatchString(p.issue) {
		p.e.GH.Run("issue", "comment", p.issue, "--repo", p.repo, "--body", body)
	}
}

func (p *parser) react(content string) {
	if id := p.e.Getenv("COMMENT_ID"); id != "" && numberRE.MatchString(id) {
		p.e.GH.Run("api", "-X", "POST", "repos/"+p.repo+"/issues/comments/"+id+"/reactions", "-f", "content="+content)
	}
}

func (p *parser) fail(msg string) int {
	p.r.Error(msg)
	p.reply("❌ " + clean(msg))
	p.react("confused")
	p.r.SetOutput("ok", "false")
	return 1
}

func (p *parser) ignore(msg string) int {
	fmt.Fprintln(p.e.Stdout, msg)
	p.reply("ℹ️ " + clean(msg))
	p.react("confused")
	p.r.SetOutput("ok", "false")
	return 0
}

// splitBlank splits on spaces and tabs only, like `read` and word splitting with the default IFS minus newlines.
func splitBlank(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '\t' })
}

// Parse reads the event of the step from the environment and writes the outputs ok, cmd, names, targets, sha, issue.
func Parse(e Env) int {
	g := e.Getenv
	p := &parser{e: e, r: &ghactions.Runner{Getenv: g, Stdout: e.Stdout}, repo: g("REPO"), issue: g("ISSUE_NUMBER")}
	event := g("EVENT")
	if event == "" {
		fmt.Fprintln(e.Stderr, "drift-fix: EVENT is required")
		return 1
	}
	if event == "issue_comment" && g("IS_PR") == "true" { // pull requests are handled by comment-ops
		p.r.SetOutput("ok", "false")
		return 0
	}
	actor := g("ACTOR")
	perm := "none"
	if actorRE.MatchString(actor) {
		if out, err := e.GH.Run("api", "repos/"+p.repo+"/collaborators/"+actor+"/permission", "--jq", ".permission"); err == nil {
			perm = strings.TrimSpace(out)
		}
	}
	if perm != "admin" && perm != "write" && perm != "maintain" {
		return p.fail(actor + " lacks write access")
	}

	cmd, names := "", ""
	var targets []string
	if event == "issue_comment" {
		m := titleRE.FindStringSubmatch(g("ISSUE_TITLE"))
		if m == nil {
			return p.ignore("/tofu commands on an issue work only on a `Drift: <workspace>` issue (comment `/tofu plan`, `/tofu apply` or `/tofu fix` there); to change code open a pull request")
		}
		names = m[1]
		// the drift issue is made by this pipeline (a bot) and carries its label: a stranger's issue with the same title is not one
		var labels []string
		json.Unmarshal([]byte(g("ISSUE_LABELS")), &labels)
		has := false
		for _, l := range labels {
			has = has || l == "tfpls-drift"
		}
		if g("ISSUE_USER_TYPE") != "Bot" || !has {
			return p.ignore("this is not a drift issue made by the pipeline (it must be opened by the bot and carry the `tfpls-drift` label)")
		}
		if st := g("ISSUE_STATE"); st != "" && st != "open" {
			return p.ignore("this drift issue is closed: the workspace is back in sync")
		}
		p.react("eyes")
		line := strings.ReplaceAll(strings.SplitN(g("BODY"), "\n", 2)[0], "\r", "")
		f := splitBlank(line)
		var rest []string
		if len(f) > 1 {
			cmd, rest = f[1], f[2:]
		}
		if cmd != "plan" && cmd != "apply" && cmd != "fix" {
			return p.fail("unknown command: " + cmd + " (use plan, apply or fix)")
		}
		for _, tok := range rest {
			v := strings.TrimPrefix(tok, "-target=")
			if strings.HasPrefix(tok, "-target=") && targetRE.MatchString(v) {
				targets = append(targets, v)
			} else {
				return p.fail("unexpected argument: " + tok + " (a drift issue is one workspace: only -target=<address> is allowed)")
			}
		}
	} else {
		cmd = g("IN_STEP")
		if cmd == "" {
			cmd = "fix"
		}
		if cmd != "plan" && cmd != "apply" && cmd != "fix" {
			return p.fail("unknown step: " + cmd + " (use plan, apply or fix)")
		}
		var ns []string
		for _, tok := range splitBlank(strings.NewReplacer(",", " ", "\n", " ", "\r", " ").Replace(g("IN_WORKSPACES"))) {
			if !nameRE.MatchString(tok) {
				return p.fail("unexpected workspace name: " + tok + " (exact names only, no globs)")
			}
			ns = append(ns, tok)
		}
		names = strings.Join(ns, " ")
		if names == "" {
			return p.fail("name the workspace(s) to fix: the input workspaces")
		}
	}
	def := g("DEFAULT_BRANCH")
	sha := ""
	if branchRE.MatchString(def) {
		if out, err := e.GH.Run("api", "repos/"+p.repo+"/commits/"+def, "--jq", ".sha"); err == nil {
			sha = strings.TrimSpace(out)
		}
	}
	if sha == "" {
		return p.fail("could not read the default branch " + def)
	}
	tg := strings.Join(targets, " ")
	for _, kv := range [][2]string{{"ok", "true"}, {"cmd", cmd}, {"names", names}, {"targets", tg}, {"sha", sha}, {"issue", p.issue}} {
		p.r.SetOutput(kv[0], kv[1])
	}
	extra := ""
	if tg != "" {
		extra = " (targets: " + tg + ")"
	}
	fmt.Fprintf(e.Stdout, "drift fix: %s on %s from %s%s\n", cmd, names, sha, extra)
	return 0
}

type entry struct {
	label string
	raw   json.RawMessage
}

// ordered reads a JSON object keeping the order of its keys (what the former shell version printed).
func ordered(s string) ([]entry, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, fmt.Errorf("not an object")
	}
	var out []entry
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		out = append(out, entry{k.(string), raw})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data")
	}
	return out, nil
}

// forbids says whether an entry switches the fix off: drift_fix "false" (detect-workspaces prints a string) or the JSON value false.
func forbids(raw json.RawMessage) bool {
	var v struct {
		DriftFix any `json:"drift_fix"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	switch x := v.DriftFix.(type) {
	case string:
		return x == "false"
	case bool:
		return !x
	}
	return false
}

// Filter drops the workspaces that forbid a fix from ENTRIES and says so on the issue. Outputs: matrix, entries, any.
func Filter(e Env) int {
	g := e.Getenv
	r := &ghactions.Runner{Getenv: g, Stdout: e.Stdout}
	all, err := ordered(g("ENTRIES"))
	if err != nil {
		r.Error("the entries of detect-workspaces are not a JSON object: " + err.Error())
		return 1
	}
	var blocked, labels []string
	var kept bytes.Buffer
	kept.WriteByte('{')
	for _, en := range all {
		if forbids(en.raw) {
			var v struct {
				Label string `json:"label"`
			}
			json.Unmarshal(en.raw, &v)
			blocked = append(blocked, v.Label)
			continue
		}
		if len(labels) > 0 {
			kept.WriteByte(',')
		}
		k, _ := json.Marshal(en.label)
		kept.Write(k)
		kept.WriteByte(':')
		json.Compact(&kept, en.raw)
		labels = append(labels, en.label)
	}
	kept.WriteByte('}')
	if len(blocked) > 0 {
		msg := "🚫 `" + strings.Join(blocked, " ") + "`: fixing a drift on demand is switched off for this workspace (`drift_fix: false`); open a pull request instead."
		r.Warning(msg)
		if n := g("ISSUE_NUMBER"); numberRE.MatchString(n) {
			e.GH.Run("issue", "comment", n, "--repo", g("REPO"), "--body", msg)
		}
	}
	sort.Strings(labels)
	inc := make([]map[string]string, 0, len(labels))
	for _, l := range labels {
		inc = append(inc, map[string]string{"label": l})
	}
	matrix, _ := json.Marshal(map[string]any{"include": inc})
	anyv := "false"
	if len(labels) > 0 {
		anyv = "true"
	}
	r.SetOutput("entries", kept.String())
	r.SetOutput("matrix", string(matrix))
	r.SetOutput("any", anyv)
	return 0
}
