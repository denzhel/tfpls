// Package driftreport remembers each workspace's last drift state on the data branch (drift/<name>.json) and acts only on changes:
//
//	in-sync -> drift        transition "drifted"    (open or refresh the workspace's issue)
//	drift/error -> in-sync  transition "recovered"  (comment and close the issue)
//	anything -> error       transition "failing"    (once, not on every run)
//
// Best effort like the audit log: it never fails the run. The transition "unstored" means the state could not be written, so the
// workflow alerts on any non-clean result.
package driftreport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/denzhel/tfpls/internal/datastore"
	"github.com/denzhel/tfpls/internal/gh"
)

// Env is injected for tests.
type Env struct {
	Getenv    func(string) string
	Stdout    io.Writer
	GH        gh.Client
	SetOutput func(name, value string) error
	Now       func() time.Time
}

var (
	branchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	digitsRe = regexp.MustCompile(`^[0-9]+$`)
)

type state struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Since   string `json:"since"`
	Checked string `json:"checked"`
	Issue   *int64 `json:"issue"`
	Summary string `json:"summary"`
}

// fileName is drift/<name>.json with "/" and " " as "_" and every other character outside [A-Za-z0-9._-] dropped.
func fileName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == ' ':
			b.WriteByte('_')
		case r < 128 && (r == '.' || r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'):
			b.WriteRune(r)
		}
	}
	return "drift/" + b.String() + ".json"
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// planLine is "N to add, N to change, N to destroy." from the plan's own last "Plan: ..." line ("" when there is none).
func planLine(text string) string {
	line := ""
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "Plan: ") {
			line = strings.TrimPrefix(l, "Plan: ")
		}
	}
	return line
}

// clipPlan keeps the beginning and the end (the "Plan: N to add..." summary is at the end) of a plan longer than max bytes, with a
// note in between. The output is always valid UTF-8 (a cut in the middle of a character is dropped).
func clipPlan(text string, max int) string {
	if len(text) <= max {
		return strings.ToValidUTF8(text, "")
	}
	head := max * 6 / 10
	tail := max - head
	out := text[:head] + fmt.Sprintf("\n\n[... %d bytes omitted: the plan is longer than a pull request comment allows; the full text is in the run logs ...]\n\n", len(text)-head-tail) + text[len(text)-tail:]
	return strings.ToValidUTF8(out, "")
}

// Run implements `tfpls drift-report`. It always returns 0.
func Run(e Env) int {
	g := e.Getenv
	out := func(k, v string) {
		if e.SetOutput != nil {
			_ = e.SetOutput(k, v)
		}
	}
	say := func(f string, a ...any) { fmt.Fprintf(e.Stdout, f+"\n", a...) }
	giveUp := func(why, transition string) int {
		say("::warning::drift report: %s", why)
		out("transition", transition)
		return 0
	}
	name, st := g("NAME"), g("STATE")
	branch := g("BRANCH")
	if branch == "" {
		branch = "tfpls-data"
	}
	now := g("TFPLS_NOW")
	if now == "" {
		now = e.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	label := g("LABEL")
	if label == "" {
		label = name
	}
	switch st {
	case "in-sync", "drift", "error":
	default:
		return giveUp("unknown state `"+st+"`", "none")
	}
	if branch == "main" || branch == "master" {
		return giveUp("refusing to write to `"+branch+"`", "none")
	}
	if !branchRe.MatchString(branch) {
		return giveUp("invalid branch name", "none")
	}
	file := fileName(name)
	summary := clip(strings.NewReplacer("\r", " ", "\n", " ").Replace(g("SUMMARY")), 300)

	// the writer runs on a fresh clone of the data branch on every attempt (a lost race starts over from the new tip)
	prev, since, issue, transition := "in-sync", now, "", "none"
	write := func(dir string) error {
		prev, since, issue, transition = "in-sync", now, "", "none"
		if b, err := os.ReadFile(filepath.Join(dir, file)); err == nil {
			var old struct {
				State string          `json:"state"`
				Since string          `json:"since"`
				Issue json.RawMessage `json:"issue"`
			}
			if json.Unmarshal(b, &old) == nil { // a corrupt file is read as "in-sync, nothing known" like before
				if old.State != "" {
					prev = old.State
				}
				if old.Since != "" {
					since = old.Since
				}
				issue = strings.Trim(string(old.Issue), `"`)
				if !digitsRe.MatchString(issue) {
					issue = ""
				}
			}
		}
		if prev != st {
			since = now
			transition = map[string]string{"drift": "drifted", "in-sync": "recovered", "error": "failing"}[st]
		}
		s := state{Name: name, State: st, Since: since, Checked: now, Summary: summary}
		if issue != "" {
			n, _ := strconv.ParseInt(issue, 10, 64)
			s.Issue = &n
		}
		if err := writeJSON(filepath.Join(dir, file), s); err != nil {
			return err
		}
		readme := filepath.Join(dir, "README.md")
		if _, err := os.Stat(readme); err != nil {
			return os.WriteFile(readme, []byte("# tfpls data\n\nState written by tfpls (`audit/` events, `drift/` last known drift state). Do not edit by hand.\n"), 0o644)
		}
		return nil
	}
	store := datastore.FromEnv(g)
	store.Branch = branch
	if err := store.Write("drift: "+name+" "+st, write); err != nil {
		return giveUp("could not store the drift state after 10 attempts", "unstored")
	}
	out("transition", transition)

	// ---- issue lifecycle (after the state is stored, so a lost race never opens two)
	repo, runURL := g("REPO"), g("RUN_URL")
	if v := g("ISSUES"); (v == "" || v == "true") && (transition != "none" || st == "drift") {
		issue = e.lifecycle(repo, runURL, name, label, st, now, since, summary, g("PLAN_TEXT"), issue, file, store, say)
	}
	out("issue", issue)
	say("drift: %s %s -> %s (transition: %s)", name, prev, st, transition)
	return 0
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

func (e Env) lifecycle(repo, runURL, name, label, st, now, since, summary, planText, issue, file string, store datastore.Store, say func(string, ...any)) string {
	title := "Drift: " + label
	// the plan, laid out like a plan comment: the summary line and the plan in one collapsed section (nothing without a plan text)
	planBlock := ""
	if planText != "" {
		text := planText + "\n"
		pl := ""
		if l := planLine(text); l != "" {
			pl = "**Plan:** " + l + "\n"
		}
		planBlock = "\n" + pl + "\n<details><summary>Click here to expand</summary>\n\n```terraform\n" + strings.TrimRight(clipPlan(text, 45000), "\n") + "\n```\n</details>"
	}
	sum := summary
	if sum == "" {
		sum = "see the run"
	}
	body := "**`" + label + "`** has drifted from its code.\n\n| | |\n|---|---|\n| Since | " + since + " |\n| Last check | " + now + " |\n| Plan | " + sum + " |\n" + planBlock
	openState := ""
	if issue != "" {
		if s, err := e.GH.Run("issue", "view", issue, "--repo", repo, "--json", "state", "--jq", ".state"); err == nil {
			openState = strings.TrimSpace(s)
		}
	}
	switch st {
	case "drift":
		if openState == "OPEN" {
			if _, err := e.GH.Run("issue", "edit", issue, "--repo", repo, "--body", body); err != nil {
				say("::warning::drift report: could not refresh issue #%s", issue)
			}
			return issue
		}
		_, _ = e.GH.Run("label", "create", "tfpls-drift", "--repo", repo, "--color", "D93F0B", "--description", "Workspace drifted from code")
		// an open drift issue of this workspace may exist without being recorded (a lost write): reuse it instead of opening a second one
		existing := ""
		var list []struct {
			Number int64  `json:"number"`
			Title  string `json:"title"`
		}
		if err := e.GH.JSON(&list, "issue", "list", "--repo", repo, "--state", "open", "--label", "tfpls-drift", "--limit", "100", "--json", "number,title"); err == nil {
			for _, it := range list {
				if it.Title == title {
					existing = strconv.FormatInt(it.Number, 10)
					break
				}
			}
		}
		url := ""
		if existing != "" {
			if _, err := e.GH.Run("issue", "edit", existing, "--repo", repo, "--body", body); err != nil {
				say("::warning::drift report: could not refresh issue #%s", existing)
			}
			url = "https://github.com/" + repo + "/issues/" + existing
		} else if s, err := e.GH.Run("issue", "create", "--repo", repo, "--title", title, "--body", body, "--label", "tfpls-drift"); err == nil {
			url = strings.TrimSpace(s)
		}
		issue = url[strings.LastIndex(url, "/")+1:]
		if !digitsRe.MatchString(issue) {
			say("::warning::drift report: could not open an issue")
			return ""
		}
		// store the issue number (best effort: a second attempt just reuses the open issue by title lookup next time)
		n, _ := strconv.ParseInt(issue, 10, 64)
		err := store.Write("drift: "+name+" issue #"+issue, func(dir string) error {
			p := filepath.Join(dir, file)
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			var s state
			if err := json.Unmarshal(b, &s); err != nil {
				return err
			}
			s.Issue = &n
			return writeJSON(p, s)
		})
		if err != nil {
			say("::warning::drift report: could not store the issue number")
		}
	case "in-sync":
		if openState == "OPEN" {
			if _, err := e.GH.Run("issue", "close", issue, "--repo", repo, "--comment", "Back in sync as of "+now+" ([run]("+runURL+"))."); err != nil {
				say("::warning::drift report: could not close issue #%s", issue)
			}
		}
	case "error":
		if openState == "OPEN" {
			_, _ = e.GH.Run("issue", "comment", issue, "--repo", repo, "--body", "The drift check itself failed on "+now+" ([run]("+runURL+")); the workspace may still be drifted.")
		}
	}
	return issue
}
