package glue

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denzhel/tfpls/internal/gh"
)

func run(t *testing.T, sub string, c gh.Client, env map[string]string, mod func(*Env)) (int, string, string) {
	t.Helper()
	d := t.TempDir()
	env["GITHUB_OUTPUT"] = filepath.Join(d, "out")
	env["GITHUB_STEP_SUMMARY"] = filepath.Join(d, "sum")
	var so, se bytes.Buffer
	e := Env{Getenv: func(k string) string { return env[k] }, Stdout: &so, Stderr: &se, GH: c}
	if mod != nil {
		mod(&e)
	}
	rc := Main([]string{sub}, e)
	o, _ := os.ReadFile(env["GITHUB_OUTPUT"])
	s, _ := os.ReadFile(env["GITHUB_STEP_SUMMARY"])
	return rc, string(o) + "|" + string(s), so.String()
}

func ghPrinting(t *testing.T, answer string) gh.Client {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "a"), []byte(answer), 0o644)
	os.WriteFile(filepath.Join(d, "gh"), []byte("#!/bin/sh\ncat \"$(dirname \"$0\")/a\"\n"), 0o755)
	return gh.Client{Bin: filepath.Join(d, "gh")}
}

func TestForkCheck(t *testing.T) {
	rc, files, out := run(t, "fork-check", gh.Client{}, map[string]string{"GITHUB_REPOSITORY": "o/r", "HEAD_REPO": "x/r"}, nil)
	if rc != 0 || !strings.Contains(files, "is_fork=true") || !strings.Contains(files, "### Fork pull request not planned\nThis pull request comes from a fork (x/r).") ||
		!strings.HasPrefix(out, "::notice title=Fork pull request not planned::This pull request comes from a fork (x/r). Fork pull requests receive no credentials") {
		t.Fatalf("%q %q", files, out)
	}
	for _, head := range []string{"o/r", ""} {
		_, files, _ = run(t, "fork-check", gh.Client{}, map[string]string{"GITHUB_REPOSITORY": "o/r", "HEAD_REPO": head}, nil)
		if !strings.Contains(files, "is_fork=false") {
			t.Fatalf("%q: %q", head, files)
		}
	}
}

func TestDriftState(t *testing.T) {
	for s, want := range map[string]string{"no-changes": "in-sync", "applied": "in-sync", "planned": "drift", "skipped": "drift", "failed": "error", "": "error", "applied ": "error"} {
		_, files, _ := run(t, "drift-state", gh.Client{}, map[string]string{"S": s}, nil)
		if !strings.Contains(files, "state="+want+"\n") {
			t.Fatalf("%q: %q", s, files)
		}
	}
}

func TestMergedPR(t *testing.T) {
	env := func() map[string]string { return map[string]string{"GITHUB_REPOSITORY": "o/r", "SHA": "abc123"} }
	rc, files, out := run(t, "merged-pr", ghPrinting(t, `[{"number":8,"merged_at":null,"head":{"sha":"cafe00"}},{"number":7,"merged_at":"2026-10-19T00:00:00Z","head":{"sha":"deadbeef"}}]`), env(), nil)
	if rc != 0 || !strings.Contains(files, "head_sha=deadbeef\n") || !strings.Contains(files, "pr_number=7\n") || !strings.Contains(out, "merged PR: #7 (head SHA deadbeef)") {
		t.Fatalf("%q %q", files, out)
	}
	for _, answer := range []string{`[]`, `not json`, `[{"number":8,"merged_at":null,"head":{"sha":"cafe00"}}]`} {
		_, files, out = run(t, "merged-pr", ghPrinting(t, answer), env(), nil)
		if !strings.Contains(files, "head_sha=\n") || !strings.Contains(files, "pr_number=\n") || !strings.Contains(out, "none, direct push") {
			t.Fatalf("%s: %q %q", answer, files, out)
		}
	}
	bad := env()
	bad["SHA"] = "x/../y"
	if rc, _, _ := run(t, "merged-pr", gh.Client{}, bad, nil); rc != 1 {
		t.Fatal("a malformed SHA must stop")
	}
}

func TestDriftIssue(t *testing.T) {
	list := `[{"number":4,"title":"Drift: other"},{"number":9,"title":"Drift: dev"},{"number":11,"title":"Drift: dev"}]`
	e := func(given string) map[string]string {
		return map[string]string{"GITHUB_REPOSITORY": "o/r", "GIVEN": given, "LABEL": "dev"}
	}
	if _, files, _ := run(t, "drift-issue", ghPrinting(t, list), e(""), nil); !strings.Contains(files, "number=9\n") {
		t.Fatalf("%q", files)
	}
	if _, files, _ := run(t, "drift-issue", ghPrinting(t, list), e("5"), nil); !strings.Contains(files, "number=5\n") {
		t.Fatalf("a given number wins: %q", files)
	}
	if _, files, _ := run(t, "drift-issue", ghPrinting(t, "boom"), e(""), nil); !strings.Contains(files, "number=\n") {
		t.Fatalf("a failed lookup gives no number: %q", files)
	}
	if _, files, _ := run(t, "drift-issue", ghPrinting(t, `[{"number":3,"title":"Drift: dev-2"}]`), e(""), nil); !strings.Contains(files, "number=\n") {
		t.Fatalf("exact title match only: %q", files)
	}
	if rc, _, _ := run(t, "drift-issue", gh.Client{}, e("5; x"), nil); rc != 1 {
		t.Fatal("a malformed GIVEN must stop")
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

func TestStorePage(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "--bare", "--quiet", "-b", "tfpls-data", origin)
	seed := filepath.Join(root, "seed")
	os.MkdirAll(seed, 0o755)
	git(t, seed, "init", "--quiet", "-b", "tfpls-data")
	os.WriteFile(filepath.Join(seed, "events.json"), []byte("{}"), 0o644)
	git(t, seed, "add", "events.json")
	git(t, seed, "commit", "--quiet", "-m", "seed")
	git(t, seed, "push", "--quiet", origin, "tfpls-data")
	src := filepath.Join(root, "page")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "index.html"), []byte("<html>v1</html>"), 0o644)
	env := map[string]string{"GITHUB_REPOSITORY": "o/r", "TOKEN": "secret-token", "BRANCH": "tfpls-data", "SRC": src}
	mod := func(e *Env) { e.Remote = func(string) string { return origin } }
	if rc, _, out := run(t, "store-page", gh.Client{}, env, mod); rc != 0 || out != "" {
		t.Fatalf("rc=%d out=%q", rc, out)
	}
	check := filepath.Join(root, "check")
	git(t, root, "clone", "--quiet", origin, check)
	if b, _ := os.ReadFile(filepath.Join(check, "dashboard", "index.html")); string(b) != "<html>v1</html>" {
		t.Fatalf("page not stored: %q", b)
	}
	if !strings.Contains(git(t, check, "log", "--format=%an <%ae> %s", "-1"), "tfpls <tfpls@users.noreply.github.com> dashboard: refresh") {
		t.Fatal("commit identity or message")
	}
	n := strings.Count(git(t, check, "log", "--oneline"), "\n")
	run(t, "store-page", gh.Client{}, env, mod) // unchanged page: no new commit
	git(t, check, "pull", "--quiet")
	if m := strings.Count(git(t, check, "log", "--oneline"), "\n"); m != n {
		t.Fatalf("an unchanged page made a commit (%d -> %d)", n, m)
	}
	// no data branch: a warning, not a failure
	env["BRANCH"] = "missing"
	if rc, _, out := run(t, "store-page", gh.Client{}, env, mod); rc != 0 || !strings.Contains(out, "::warning::no `missing` branch to store the page on") {
		t.Fatalf("rc=%d out=%q", rc, out)
	}
	// the token never appears in a URL: the default remote has none
	env["BRANCH"] = "-x"
	if rc, _, _ := run(t, "store-page", gh.Client{}, env, mod); rc != 1 {
		t.Fatal("a branch that looks like an option must stop")
	}
}
