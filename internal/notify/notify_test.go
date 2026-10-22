package notify

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func get(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultSlack(t *testing.T) {
	r, _, _, _ := build(get(map[string]string{"STATUS": "error", "NAME": "prod", "KIND": "apply", "MENTION": "@x", "SUMMARY": strings.Repeat("ä", 600),
		"AUTHOR": "al", "COMMIT_MESSAGE": "a `b`\nsecond", "COMMIT_URL": "u", "URL": "run"}))
	got := defaultSlack(r)
	lines := strings.Split(got, "\n")
	if lines[0] != "@x A run on `prod` has failed" || lines[1] != "By @al, <u|Commit Message>: `a 'b'`" || len([]rune(lines[3])) != 301 {
		t.Fatalf("%q", got)
	}
	r, _, _, _ = build(get(map[string]string{"STATUS": "no-changes", "KIND": "apply"}))
	if defaultSlack(r) != "" {
		t.Fatal("no-changes must send nothing")
	}
}

func TestChangesCap(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < 60; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"action":"added","address":"a"}`)
	}
	sb.WriteString(`,5,"x"]`)
	r, _, _, _ := build(get(map[string]string{"STATUS": "applied", "CHANGES": sb.String(), "CHANGES_MORE": "3"}))
	if len(r.Changes) != 50 || r.ChangesMore != 10 {
		t.Fatal(len(r.Changes), r.ChangesMore)
	}
}

// A template that is a symlink out of the checkout was read by the Bash; it is refused now.
func TestTemplateSymlinkOutsideRefused(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(outside, "t.jq"), []byte(`{text:"x"}`), 0o644)
	os.Symlink(filepath.Join(outside, "t.jq"), filepath.Join(ws, "t.jq"))
	var out bytes.Buffer
	e := Env{Stdout: &out}
	if f := e.templateFile(ws, "t.jq", "", ""); f != "" || !strings.Contains(out.String(), "not a relative path") {
		t.Fatal(f, out.String())
	}
}
