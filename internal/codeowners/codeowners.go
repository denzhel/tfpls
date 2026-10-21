// Package codeowners requires an approval from a CODEOWNER of every workspace an apply touches (actions/codeowners).
// CODEOWNERS is read from the base branch (a pull request must not be able to change who has to approve it).
package codeowners

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/ghactions"
)

type review struct {
	User *struct {
		Login string `json:"login"`
	} `json:"user"`
	State       string `json:"state"`
	CommitID    string `json:"commit_id"`
	SubmittedAt string `json:"submitted_at"`
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// Approvers returns the lower-cased logins whose LATEST review (approval, change request or dismissal) is an approval of sha.
func Approvers(reviewsJSON []byte, sha string) (map[string]bool, error) {
	var rs []review
	if err := json.Unmarshal(reviewsJSON, &rs); err != nil {
		return nil, fmt.Errorf("reviews are not a JSON list of reviews: %w", err)
	}
	latest := map[string]review{}
	// a stable sort by time, so that of two reviews at the same second the later one in the list wins
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].SubmittedAt < rs[j].SubmittedAt })
	for _, r := range rs {
		if r.User == nil || r.User.Login == "" {
			continue // fail closed: a review without an author counts for nobody
		}
		if r.State == "APPROVED" || r.State == "CHANGES_REQUESTED" || r.State == "DISMISSED" {
			latest[asciiLower(r.User.Login)] = r
		}
	}
	out := map[string]bool{}
	for login, r := range latest {
		if r.State == "APPROVED" && r.CommitID == sha {
			out[login] = true
		}
	}
	return out, nil
}

type rule struct {
	re     *regexp.Regexp
	owners []string
}

const (
	phDirs = "\x01"
	phAny  = "\x02"
)

func patternRegexp(pat string) *regexp.Regexp {
	body := strings.TrimPrefix(pat, "/")
	var b strings.Builder
	for _, r := range body {
		if strings.ContainsRune(`.+^${}()|[]\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	g := b.String()
	g = strings.ReplaceAll(g, "**/", phDirs)
	g = strings.ReplaceAll(g, "**", phAny)
	g = strings.ReplaceAll(g, "*", "[^/]*")
	g = strings.ReplaceAll(g, "?", "[^/]")
	g = strings.ReplaceAll(g, phDirs, "(.*/)?")
	g = strings.ReplaceAll(g, phAny, ".*")
	prefix := "^(.*/)?"
	if strings.HasPrefix(pat, "/") {
		prefix = "^"
	} else if i := strings.Index(pat, "/"); i >= 0 && i < len(pat)-1 {
		prefix = "^" // a slash in the middle anchors the pattern at the root
	}
	suffix := "(/.*)?$"
	if strings.HasSuffix(pat, "/") {
		suffix = ".*$"
	}
	re, err := regexp.Compile(prefix + g + suffix)
	if err != nil {
		return nil
	}
	return re
}

// ParseRules reads a CODEOWNERS file: a pattern and at least one owner per line, `#` starts a comment.
func ParseRules(content string) []rule {
	var rules []rule
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t\r\f\v"), "#") {
			continue
		}
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if re := patternRegexp(f[0]); re != nil {
			rules = append(rules, rule{re, f[1:]})
		}
	}
	return rules
}

// OwnersOf returns the owners of the LAST rule that matches the path (a workspace directory), none when no rule matches.
func OwnersOf(rules []rule, path string) []string {
	p := strings.TrimSuffix(path, "/") + "/x"
	var owners []string
	for _, r := range rules {
		if r.re.MatchString(p) {
			owners = r.owners
		}
	}
	return owners
}

// Teams resolves the members of a team owner ("@org/team").
type Teams func(team string) []string

var reSlug = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// GHTeams reads the members with `gh api`; $TFPLS_TEAMS_FILE (lines "@org/team alice bob") replaces the API in tests.
// A failed lookup yields no members: the team does not count (fail closed).
func GHTeams(c gh.Client, getenv func(string) string) Teams {
	return func(team string) []string {
		if f := getenv("TFPLS_TEAMS_FILE"); f != "" {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil
			}
			for _, line := range strings.Split(string(b), "\n") {
				if w := strings.Fields(line); len(w) > 0 && w[0] == team {
					return w[1:]
				}
			}
			return nil
		}
		org, slug, ok := strings.Cut(strings.TrimPrefix(team, "@"), "/")
		if !ok || !reSlug.MatchString(org) || !reSlug.MatchString(slug) {
			return nil
		}
		out, err := c.Run("api", "--paginate", "orgs/"+org+"/teams/"+slug+"/members", "--jq", ".[].login")
		if err != nil {
			return nil
		}
		return strings.Fields(out)
	}
}

// Check decides whether every path is satisfied: one of its owners has approved the current commit. It returns the
// message to show (what is missing, or that all is fine) and ok.
func Check(co string, reviewsJSON []byte, sha string, paths []string, teams Teams) (string, bool, error) {
	approvers, err := Approvers(reviewsJSON, sha)
	if err != nil {
		return "", false, err
	}
	rules := ParseRules(co)
	var missing strings.Builder
	for _, path := range paths {
		owners := OwnersOf(rules, path)
		if len(owners) == 0 {
			continue
		}
		satisfied := false
		for _, who := range owners {
			switch {
			case strings.HasPrefix(who, "@") && strings.Contains(who, "/"):
				for _, m := range teams(who) {
					if approvers[asciiLower(m)] {
						satisfied = true
						break
					}
				}
			case strings.HasPrefix(who, "@"):
				satisfied = approvers[asciiLower(strings.TrimPrefix(who, "@"))]
			}
			// e-mail owners cannot be matched to reviews
			if satisfied {
				break
			}
		}
		if !satisfied {
			fmt.Fprintf(&missing, "- `%s` needs an approval of the current commit from one of: %s\n", path, strings.Join(owners, " "))
		}
	}
	if missing.Len() > 0 {
		return "CODEOWNERS approval is missing:\n" + missing.String(), false, nil
	}
	return "code owners have approved", true, nil
}

// RunCheck is `tfpls codeowners check <CODEOWNERS> <reviews.json> <sha> <path>...`. Exit: 0 satisfied, 1 missing
// approvals (explained on stdout), 2 usage or unreadable input.
func RunCheck(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) < 4 || args[2] == "" {
		fmt.Fprintln(stderr, "usage: tfpls codeowners check <CODEOWNERS> <reviews.json> <sha> <path>...")
		return 2
	}
	co, err1 := os.ReadFile(args[0])
	rv, err2 := os.ReadFile(args[1])
	if err1 != nil || err2 != nil {
		fmt.Fprintln(stderr, "usage: tfpls codeowners check <CODEOWNERS> <reviews.json> <sha> <path>... (both files must exist)")
		return 2
	}
	msg, ok, err := Check(string(co), rv, args[2], args[3:], GHTeams(gh.Client{}, getenv))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintln(stdout, strings.TrimSuffix(msg, "\n"))
	if !ok {
		return 1
	}
	return 0
}

// Env is what Enforce needs from outside.
type Env struct {
	Getenv  func(string) string
	Runner  *ghactions.Runner
	GH      gh.Client
	Stdout  io.Writer
	Reviews func(repo, pr string) ([]byte, error) // the reviews that may count (people with write access)
}

// DefaultEnv is the real environment. The reviews come from actions/approvals/reviews.sh next to this action.
func DefaultEnv() Env {
	return Env{Getenv: os.Getenv, Runner: ghactions.New(), Stdout: os.Stdout, Reviews: func(repo, pr string) ([]byte, error) {
		dir := os.Getenv("GITHUB_ACTION_PATH")
		return exec.Command(filepath.Join(dir, "..", "approvals", "reviews.sh"), repo, pr).Output()
	}}
}

var reNum = regexp.MustCompile(`^[0-9]+$`)

// OwnerPaths lists the distinct non-empty owner_paths of the entries (an object keyed by label, or a list), sorted.
func OwnerPaths(entries string) ([]string, error) {
	var any_ any
	if err := json.Unmarshal([]byte(entries), &any_); err != nil {
		return nil, fmt.Errorf("ENTRIES is not valid JSON: %w", err)
	}
	var items []any
	switch t := any_.(type) {
	case map[string]any:
		for _, v := range t {
			items = append(items, v)
		}
	case []any:
		items = t
	}
	set := map[string]bool{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		s, _ := m["owner_paths"].(string)
		for _, p := range strings.Split(s, "\n") {
			if p != "" {
				set[p] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// Enforce is `tfpls codeowners enforce` (env: REPO, BASE, PR, SHA, ENTRIES, COMMENT_ID, GH_TOKEN): it fetches CODEOWNERS from
// the base branch tip, checks the workspace paths of the entries and, on a refusal, explains it on the pull request.
func Enforce(e Env) int {
	repo, base, pr, sha := e.Getenv("REPO"), e.Getenv("BASE"), e.Getenv("PR"), e.Getenv("SHA")
	comment := func(body string) { _, _ = e.GH.Run("pr", "comment", pr, "--repo", repo, "--body", body) }
	found, co := false, ""
	// only a 404 means "no such file"; any other failure (API error, rate limit) must not read as "nothing to enforce"
	for _, f := range []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"} {
		out, err := e.GH.Run("api", "repos/"+repo+"/contents/"+f+"?ref="+url.QueryEscape(base), "-H", "Accept: application/vnd.github.raw")
		if err == nil {
			found, co = true, out
			break
		}
		if s := err.Error(); !strings.Contains(s, "404") && !strings.Contains(s, "Not Found") {
			e.Runner.Error(fmt.Sprintf("could not read %s on the base branch: %s", f, strings.TrimSpace(s)))
			comment("⛔ **apply refused.** Could not read the CODEOWNERS file on the default branch (a GitHub API error, not a missing file). Try again in a minute.")
			return 1
		}
	}
	if !found {
		fmt.Fprintln(e.Stdout, "no CODEOWNERS file on the base branch: nothing to enforce")
		return 0
	}
	reviews, err := e.Reviews(repo, pr)
	if err != nil || len(strings.TrimSpace(string(reviews))) == 0 {
		reviews = []byte("[]") // no reviews could be read: nobody has approved
	}
	paths, err := OwnerPaths(e.Getenv("ENTRIES"))
	var msg string
	ok := false
	switch {
	case err != nil:
		msg = err.Error()
	case len(paths) == 0:
		msg = "no workspace paths to check CODEOWNERS for"
	default:
		msg, ok, err = Check(co, reviews, sha, paths, GHTeams(e.GH, e.Getenv))
		if err != nil {
			msg, ok = err.Error(), false
		}
	}
	if ok {
		fmt.Fprintln(e.Stdout, msg)
		return 0
	}
	e.Runner.Error(msg)
	comment("⛔ **apply refused.** " + msg)
	if id := e.Getenv("COMMENT_ID"); reNum.MatchString(id) {
		_, _ = e.GH.Run("api", "-X", "POST", "repos/"+repo+"/issues/comments/"+id+"/reactions", "-f", "content=-1")
	}
	return 1
}
