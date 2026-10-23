package gate

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func gateEnvFor(t *testing.T, extra map[string]string) (Env, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	m := map[string]string{"REPO": "o/r", "ISSUE": "7", "EVENT_NAME": "issues", "GITHUB_OUTPUT": filepath.Join(dir, "out"),
		"AGENT_WORK": dir, "TFPLS_NOW": "2026-10-17T12:00:00Z"}
	for k, v := range extra {
		m[k] = v
	}
	var so bytes.Buffer
	gh := fakeGH(map[string]string{
		"repos/o/r/issues/7":                                    `{"number":7,"title":"t","body":"thin","author_association":"OWNER","user":{"login":"me"},"labels":[]}`,
		"repos/o/r/issues/7/comments":                           `[]`,
		"repos/o/r/actions/workflows/claude-elaborate.yml/runs": `{"workflow_runs":[]}`,
	})
	return Env{Getenv: func(k string) string { return m[k] }, GH: gh, Stdout: &so, Stderr: io.Discard}, &so, m["GITHUB_OUTPUT"]
}

// a cap variable that is not an integer, or a clock that is not ISO, is a red gate and no run (the Bash gate let the run through)
func TestRunFailsClosed(t *testing.T) {
	for name, extra := range map[string]map[string]string{"limit": {"MAX_ELAB_ISSUE": "three"}, "daily limit": {"MAX_ELAB_DAY": "x"}, "clock": {"TFPLS_NOW": "now"}} {
		env, so, out := gateEnvFor(t, extra)
		if rc := Run([]string{"elaborate"}, env); rc != 1 {
			t.Errorf("%s: rc = %d, want 1", name, rc)
		}
		if o, _ := os.ReadFile(out); strings.Contains(string(o), "run=true") {
			t.Errorf("%s: the gate said run=true: %s", name, o)
		}
		if !strings.Contains(so.String(), "::error::") {
			t.Errorf("%s: no ::error:: annotation: %s", name, so)
		}
	}
	env, _, _ := gateEnvFor(t, nil)
	env.GH = fakeGH(map[string]string{"repos/o/r/issues/7": `{"number":7,"author_association":"OWNER","user":{"login":"me"},"labels":[],"body":"x"}`})
	if rc := Run([]string{"elaborate"}, env); rc != 1 {
		t.Errorf("unreadable comments: rc = %d, want 1", rc)
	}
}

// a label is matched exactly: a label that merely contains "ready" (or a comma-joined list) is not `ready`
func TestLabelsExact(t *testing.T) {
	env, so, out := gateEnvFor(t, nil)
	full := `### Outcome\n\nSomething.\n\n### Tasks\n\n- [ ] do a\n\n### Acceptance criteria\n\n- [ ] do b\n\n### Test criteria\n\n- [ ] c d`
	env.GH = fakeGH(map[string]string{
		"repos/o/r/issues/7":          `{"number":7,"title":"t","body":"` + full + `","author_association":"OWNER","user":{"login":"me"},"labels":[{"name":"type:feature,ready"},{"name":"already"}]}`,
		"repos/o/r/issues/7/comments": `[]`,
	})
	if rc := Run([]string{"elaborate"}, env); rc != 0 {
		t.Fatalf("rc = %d: %s", rc, so)
	}
	if o, _ := os.ReadFile(out); !strings.Contains(string(o), "label=ready") {
		t.Errorf("a filled-in issue whose labels only look like ready was not labelled ready: %s", o)
	}
}

func TestReportCountsCharacters(t *testing.T) {
	long := strings.Repeat("é", 1000)
	if got := []rune(lastWords(map[string]any{"result": long})); len(got) != 900 {
		t.Errorf("lastWords = %d characters, want 900", len(got))
	}
	if got := lastWords(map[string]any{"result": "short\n\n"}); got != "short" {
		t.Errorf("lastWords = %q", got)
	}
	l, _ := denialLine(map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": strings.Repeat("é", 200) + "\nx"}})
	if n := utf8.RuneCountInString(l); n != len("- `Bash`: ")+110 {
		t.Errorf("denialLine = %d characters, want the 110-character cut", n)
	}
}
