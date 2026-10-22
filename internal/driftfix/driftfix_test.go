package driftfix

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denzhel/tfpls/internal/gh"
)

func run(t *testing.T, fn func(Env) int, kv map[string]string, sha string) (int, string, string) {
	dir := t.TempDir()
	outf := filepath.Join(dir, "out")
	bin := filepath.Join(dir, "gh")
	os.WriteFile(bin, []byte("#!/bin/sh\ncase \"$*\" in *permission*) echo write;; *commits*) echo '"+sha+"';; esac\n"), 0o755)
	kv["GITHUB_OUTPUT"] = outf
	var out bytes.Buffer
	rc := fn(Env{Getenv: func(k string) string { return kv[k] }, Stdout: &out, Stderr: &out, GH: gh.Client{Bin: bin}})
	b, _ := os.ReadFile(outf)
	return rc, string(b), out.String()
}

func TestFilterDropsStringAndBooleanFalse(t *testing.T) {
	e := `{"a":{"label":"a","drift_fix":"true"},"b":{"label":"b","drift_fix":"false"},"c":{"label":"c","drift_fix":false},"d":{"label":"d"}}`
	rc, out, _ := run(t, Filter, map[string]string{"ENTRIES": e, "REPO": "o/r"}, "")
	if rc != 0 || !strings.Contains(out, `entries={"a":{"label":"a","drift_fix":"true"},"d":{"label":"d"}}`) ||
		!strings.Contains(out, `matrix={"include":[{"label":"a"},{"label":"d"}]}`) || !strings.Contains(out, "any=true") {
		t.Fatal(rc, out)
	}
}

// The Bash wrote empty outputs for an unreadable ENTRIES and the job went on (any="" is falsy, so nothing ran, but green).
func TestFilterUnreadableEntriesFails(t *testing.T) {
	for _, in := range []string{"", "{x", "[]", "null", `{"a":{}} {}`} {
		if rc, out, _ := run(t, Filter, map[string]string{"ENTRIES": in}, ""); rc != 1 || out != "" {
			t.Errorf("%q: rc=%d out=%q", in, rc, out)
		}
	}
}

func TestParseRefusesEmptyShaAndOddActor(t *testing.T) {
	base := map[string]string{"EVENT": "workflow_dispatch", "ACTOR": "alice", "REPO": "o/r", "DEFAULT_BRANCH": "main", "IN_WORKSPACES": "dev"}
	if rc, out, _ := run(t, Parse, copyOf(base), ""); rc != 1 || strings.Contains(out, "ok=true") {
		t.Errorf("empty sha accepted: %d %s", rc, out)
	}
	odd := copyOf(base)
	odd["ACTOR"] = "a/../b"
	if rc, _, msg := run(t, Parse, odd, "abc"); rc != 1 || !strings.Contains(msg, "lacks write access") {
		t.Errorf("odd actor: %d %s", rc, msg)
	}
	if rc, out, _ := run(t, Parse, copyOf(base), "abc"); rc != 0 || !strings.Contains(out, "sha=abc") {
		t.Errorf("good: %d %s", rc, out)
	}
}

func copyOf(m map[string]string) map[string]string {
	c := map[string]string{}
	for k, v := range m {
		c[k] = v
	}
	return c
}

func TestSplitBlankKeepsOtherWhitespaceInTokens(t *testing.T) {
	if got := splitBlank(" /tofu\tplan  -target=a "); strings.Join(got, "|") != "/tofu|plan|-target=a" {
		t.Fatal(got)
	}
}
