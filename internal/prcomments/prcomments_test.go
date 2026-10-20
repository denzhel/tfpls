package prcomments

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denzhel/tfpls/internal/gh"
)

// stub writes a gh that lists the given JSON for comment listings and logs every other call.
func stub(t *testing.T, comments string) (gh.Client, string) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	os.WriteFile(filepath.Join(dir, "c.json"), []byte(comments), 0o644)
	script := "#!/bin/sh\ncase \"$*\" in *PATCH*|*DELETE*|*-f*) echo \"$*\" >> " + log + "; echo '{}';; *) cat " + filepath.Join(dir, "c.json") + ";; esac\n"
	bin := filepath.Join(dir, "gh")
	os.WriteFile(bin, []byte(script), 0o755)
	return gh.Client{Bin: bin}, log
}

func env(c gh.Client, kv map[string]string) (Env, *bytes.Buffer) {
	var out bytes.Buffer
	return Env{Getenv: func(k string) string { return kv[k] }, Stdout: &out, Stderr: &out, GH: c}, &out
}

const plan = `[{"id":1,"user":{"type":"Bot"},"body":"<!-- tfpls:a -->\nplan"},{"id":2,"body":"no user"}]`

func TestOutdatedMarksUnselected(t *testing.T) {
	c, log := stub(t, plan)
	e, out := env(c, map[string]string{"GITHUB_REPOSITORY": "o/r", "PR": "1", "NAMES": "[]", "SHA": "abcdef123456"})
	if Outdated(e) != 0 || !strings.Contains(out.String(), "marked outdated: a") {
		t.Fatal(out.String())
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "(`abcdef1`)") {
		t.Fatal(string(b))
	}
}

// The Bash treated an unreadable NAMES as "nothing selected" and marked every plan outdated.
func TestOutdatedUnreadableNamesMarksNothing(t *testing.T) {
	for _, names := range []string{"{not json", `{"a":1}`, "a,b"} {
		c, log := stub(t, plan)
		e, out := env(c, map[string]string{"GITHUB_REPOSITORY": "o/r", "PR": "1", "NAMES": names})
		if Outdated(e) != 0 || !strings.Contains(out.String(), "::warning::") {
			t.Fatal(names, out.String())
		}
		if _, err := os.Stat(log); err == nil {
			t.Fatal("a comment was edited for NAMES=" + names)
		}
	}
}

func TestNotesClearOnlyBot(t *testing.T) {
	c, log := stub(t, `[{"id":5,"user":{"type":"User"},"body":"<!-- tfpls:no-workspaces -->"},{"id":6,"user":{"type":"Bot"},"body":"<!-- tfpls:no-workspaces -->"}]`)
	e, _ := env(c, map[string]string{"REPO": "o/r", "PR": "1"})
	if Notes([]string{"clear"}, e) != 0 {
		t.Fatal("rc")
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "comments/6") || strings.Contains(string(b), "comments/5") {
		t.Fatal(string(b))
	}
}

func TestNotesUsage(t *testing.T) {
	e, _ := env(gh.Client{}, nil)
	if Notes([]string{"bogus"}, e) != 2 || Notes(nil, e) != 2 {
		t.Fatal("usage")
	}
}
