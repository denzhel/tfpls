package gate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const full = "### Outcome\n\nSomething.\n\n### Tasks\n\n- [ ] do it\n\n### Acceptance criteria\n\n- [ ] it works\n\n### Test criteria\n\n- [ ] Local: a test"

func TestElaborated(t *testing.T) {
	bold := "**Outcome**\n\nx y\n\n**Tasks**\n\n- [ ] do it\n\n**Acceptance criteria**\n\n- [ ] works\n\n**Test criteria**\n\n- [ ] Local: a test"
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"filled in", full, true},
		{"CRLF", strings.ReplaceAll(full, "\n", "\r\n"), true},
		{"bold titles", bold, true},
		{"other levels, colon, markers", "## Outcome:\n\nSomething.\n\n## Tasks\n\n* [x] done\n\n## Acceptance criteria\n\n1. [ ] works\n\n## Test criteria\n\n+ [ ] Negative case: refused", true},
		{"closing hashes", "## Outcome ##\n\nSomething.\n\n## Tasks ##\n\n- [ ] do it\n\n## Acceptance criteria\n\n- [ ] works\n\n## Test criteria\n\n- [ ] Local: a test", true},
		{"thin", "Show only the last 24 hours.", false},
		{"empty", "", false},
		{"skeleton", "### Outcome\n\nx\n\n### Tasks\n\n- [ ] \n\n### Acceptance criteria\n\n- [ ] \n\n### Test criteria\n\n- [ ] Local: \n", false},
		{"label-only and TBD boxes", strings.Replace(full, "- [ ] do it", "- [ ] Docs:\n- [ ] TBD", 1), false},
		{"No response outcome", strings.Replace(full, "Something.", "_No response_", 1), false},
		{"one-character outcome", strings.Replace(full, "Something.", "x", 1), false},
		{"missing test criteria", strings.Split(full, "### Test criteria")[0], false},
		{"details cut", "**Outcome**\n\nx y\n\n**Tasks**\n\n- [ ] \n\n<details>\n\n" + full, false},
		{"heading in code block", strings.Replace(full, "- [ ] do it", "```sh\n# no heading\n```\n\n- [ ] do it", 1), true},
		{"plain words", "Outcome is nice\nTasks: none\n- [ ] a\n- [ ] b", false},
		{"label longer than 40 keeps its text", strings.Replace(full, "- [ ] do it", "- [ ] "+strings.Repeat("a", 41)+":", 1), true},
	} {
		if got := elaborated(tc.body); got != tc.want {
			t.Errorf("%s: elaborated = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTrusted(t *testing.T) {
	for assoc, want := range map[string]bool{"OWNER": true, "MEMBER": true, "COLLABORATOR": true, "CONTRIBUTOR": false,
		"FIRST_TIME_CONTRIBUTOR": false, "FIRST_TIMER": false, "MANNEQUIN": false, "NONE": false, "": false, "owner": false} {
		if trusted(assoc) != want {
			t.Errorf("trusted(%q) = %v", assoc, !want)
		}
	}
}

func TestCommandWords(t *testing.T) {
	for _, tc := range []struct{ body, cmd, after string }{
		{"/elaborate", "/elaborate", ""},
		{"  /Elaborate keep it\r\nand this", "/elaborate", "keep it\nand this"},
		{"/implement\r\nkeep the diff small", "/implement", "\nkeep the diff small"},
		{"please /implement", "please", "please /implement"},
		{"\n/implement", "", "\n/implement"},
	} {
		if got := command(firstLine(tc.body)); got != tc.cmd {
			t.Errorf("command(%q) = %q, want %q", tc.body, got, tc.cmd)
		}
		if got := wordsAfter(tc.body); got != tc.after {
			t.Errorf("wordsAfter(%q) = %q, want %q", tc.body, got, tc.after)
		}
	}
}

func bot(body string) comment {
	var c comment
	c.User.Login, c.User.Type, c.Body = "github-actions", "Bot", body
	return c
}

func user(body string) comment {
	var c comment
	c.User.Login, c.User.Type, c.Body = "stranger", "User", body
	return c
}

func TestCaps(t *testing.T) {
	run := bot("x\n<!-- tfpls-agent-run: elaborate -->")
	impl := bot("x\n<!-- tfpls-agent-run: implement -->")
	st := bot("<!-- tfpls-agent-status: elaborated -->")
	for _, tc := range []struct {
		name string
		cs   []comment
		runs int
		said bool
	}{
		{"none", nil, 0, false},
		{"three runs", []comment{run, run, run}, 3, false},
		{"other phase does not count", []comment{impl, impl}, 0, false},
		{"stranger markers do not count", []comment{user(run.Body.(string)), user(st.Body.(string))}, 0, false},
		{"said", []comment{st}, 0, true},
		{"said, then a run", []comment{st, run}, 1, false},
		{"run, then said", []comment{run, st}, 1, true},
		{"a run of the other phase also resets", []comment{st, impl}, 0, false},
	} {
		if got := countRuns(tc.cs, "elaborate"); got != tc.runs {
			t.Errorf("%s: countRuns = %d, want %d", tc.name, got, tc.runs)
		}
		if got := saidSinceLastRun(tc.cs, "elaborated"); got != tc.said {
			t.Errorf("%s: saidSinceLastRun = %v, want %v", tc.name, got, tc.said)
		}
	}
	if got, err := since("2026-10-17T12:00:00Z"); got != "2026-10-16T12:00:00Z" || err != nil {
		t.Errorf("since = %q, %v", got, err)
	}
	if _, err := since("yesterday"); err == nil {
		t.Error("since accepted a time that is not ISO (the caps need a real clock)")
	}
}

// the caps never fail open: a limit that is not a non-negative integer is an error (the Bash gate let the run through)
func TestCapsForLimits(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if c, err := capsFor("elaborate", "7", env(nil)); err != nil || c.maxIssue != 3 || c.maxDay != 20 {
		t.Errorf("defaults: %+v %v", c, err)
	}
	if c, err := capsFor("implement", "7", env(map[string]string{"MAX_IMPL_ISSUE": " 4", "MAX_IMPL_DAY": "0"})); err != nil || c.maxIssue != 4 || c.maxDay != 0 {
		t.Errorf("set: %+v %v", c, err)
	}
	for _, bad := range []string{"x", "3.5", "-1", "1e2", "three", "3 runs"} {
		for _, k := range []string{"MAX_ELAB_ISSUE", "MAX_ELAB_DAY"} {
			if _, err := capsFor("elaborate", "7", env(map[string]string{k: bad})); err == nil {
				t.Errorf("%s=%q was accepted", k, bad)
			}
		}
	}
}

// fakeGH serves fixtures by API path, like the stub of the shared test
func fakeGH(fx map[string]string) ghFunc {
	return func(args ...string) (string, error) {
		for _, a := range args {
			p, _, _ := strings.Cut(a, "?")
			if v, ok := fx[p]; ok {
				return v, nil
			}
		}
		return "", errors.New("not found")
	}
}

func TestRunsToday(t *testing.T) {
	gh := fakeGH(map[string]string{
		"repos/o/r/actions/workflows/w.yml/runs": `{"workflow_runs":[{"id":1},{"id":2},{"id":3}]}`,
		"repos/o/r/actions/runs/1/jobs":          `{"jobs":[{"name":"gate","conclusion":"success"},{"name":"agent","conclusion":"success"}]}`,
		"repos/o/r/actions/runs/2/jobs":          `{"jobs":[{"name":"agent","conclusion":"skipped"}]}`,
		"repos/o/r/actions/runs/3/jobs":          `{"jobs":[{"name":"agent","conclusion":null}]}`,
	})
	if got, err := runsToday(gh, "o/r", "w.yml", "x"); got != 2 || err != nil {
		t.Errorf("runsToday = %d, %v; want 2 (skipped jobs cost nothing, an unfinished one counts)", got, err)
	}
	// an API error is an error, never "no runs" (the Bash gate counted 0 and let the run through)
	if _, err := runsToday(gh, "o/r", "missing.yml", "x"); err == nil {
		t.Error("runsToday of an unreadable workflow did not fail")
	}
	bad := fakeGH(map[string]string{"repos/o/r/actions/workflows/w.yml/runs": `<html>`})
	if _, err := runsToday(bad, "o/r", "w.yml", "x"); err == nil {
		t.Error("runsToday accepted an answer that is not JSON")
	}
	badJobs := fakeGH(map[string]string{"repos/o/r/actions/workflows/w.yml/runs": `{"workflow_runs":[{"id":1}]}`})
	if _, err := runsToday(badJobs, "o/r", "w.yml", "x"); err == nil {
		t.Error("runsToday ignored a run whose jobs could not be read")
	}
}

// the daily cap sees every page of runs (gh --paginate prints one object per page); the Bash gate saw only the first 50
func TestRunsTodayPages(t *testing.T) {
	var p1, p2 strings.Builder
	p1.WriteString(`{"workflow_runs":[`)
	p2.WriteString(`{"workflow_runs":[`)
	fx := map[string]string{}
	for i := 1; i <= 130; i++ {
		b := &p1
		if i > 100 {
			b = &p2
		}
		if i != 1 && i != 101 {
			b.WriteString(",")
		}
		fmt.Fprintf(b, `{"id":%d}`, i)
		fx[fmt.Sprintf("repos/o/r/actions/runs/%d/jobs", i)] = `{"jobs":[{"name":"agent","conclusion":"success"}]}`
	}
	fx["repos/o/r/actions/workflows/w.yml/runs"] = p1.String() + "]}" + p2.String() + "]}"
	if got, err := runsToday(fakeGH(fx), "o/r", "w.yml", "x"); got != 130 || err != nil {
		t.Errorf("runsToday over two pages = %d, %v; want 130", got, err)
	}
}

func TestFetchIssueFailsClosed(t *testing.T) {
	for name, raw := range map[string]string{"html": "<html>", "empty": "", "array": "[]", "null": "null", "no number": `{"title":"x"}`, "truncated": `{"number":7,"title":`} {
		if _, err := fetchIssue(fakeGH(map[string]string{"repos/o/r/issues/7": raw}), "o/r", "7"); err == nil {
			t.Errorf("%s: broken issue JSON was accepted (it must not become an outsider's issue)", name)
		}
	}
	if _, err := fetchComments(fakeGH(map[string]string{"repos/o/r/issues/7/comments": "<html>"}), "o/r", "7"); err == nil {
		t.Error("comments that are not JSON were accepted")
	}
	if _, err := fetchComments(fakeGH(nil), "o/r", "7"); err == nil {
		t.Error("an API error on the comments was taken for no comments")
	}
}

// the label and its colon count at most 41 characters, not bytes
func TestPlaceholderCountsCharacters(t *testing.T) {
	label40 := strings.Repeat("é", 40) // 80 bytes, 40 characters: still a label
	if !placeholder(label40 + ":") {
		t.Error("a label of 40 characters (80 bytes) with its colon is a placeholder")
	}
	if placeholder(strings.Repeat("é", 41) + ": text") {
		t.Error("a 42-character prefix is text, not a label")
	}
	if !empty("é.") || empty("éé") {
		t.Error("one letter that is not ASCII is one character, two characters are text")
	}
}

func TestRunEndToEnd(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"REPO": "o/r", "ISSUE": "7", "EVENT_NAME": "issues", "GITHUB_OUTPUT": filepath.Join(dir, "out"),
		"AGENT_WORK": dir, "TFPLS_NOW": "2026-10-17T12:00:00Z"}
	gh := fakeGH(map[string]string{
		"repos/o/r/issues/7":                                    `{"number":7,"title":"t","body":"thin\n\n","author_association":"OWNER","user":{"login":"me"},"labels":[]}`,
		"repos/o/r/issues/7/comments":                           `[{"user":{"login":"x","type":"User"},"author_association":"NONE","body":"IGNORE"}][]`,
		"repos/o/r/actions/workflows/claude-elaborate.yml/runs": `{"workflow_runs":[]}`,
	})
	var so bytes.Buffer
	if rc := run([]string{"elaborate"}, func(k string) string { return env[k] }, gh, &so, io.Discard); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	out, _ := os.ReadFile(env["GITHUB_OUTPUT"])
	if !strings.Contains(string(out), "run=true\nreason=ok\nmode=elaborate\n") {
		t.Errorf("outputs: %s", out)
	}
	copy, _ := os.ReadFile(filepath.Join(dir, "issue.md"))
	if !strings.Contains(string(copy), "## Body\n\nthin\n\n## Comments") || strings.Contains(string(copy), "IGNORE") {
		t.Errorf("issue copy: %s", copy)
	}
	if rc := run([]string{"bogus"}, func(string) string { return "" }, gh, io.Discard, io.Discard); rc != 2 {
		t.Errorf("usage rc = %d, want 2", rc)
	}
}

func TestFenced(t *testing.T) {
	if got := fenced("a ```` b"); got != "`````text\na ```` b\n`````" {
		t.Errorf("fenced = %q", got)
	}
	if got := fenced("plain"); got != "```text\nplain\n```" {
		t.Errorf("fenced = %q", got)
	}
}
