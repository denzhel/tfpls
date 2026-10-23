package comment

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denzhel/tfpls/internal/gh"
)

// stub writes a gh that answers from the files perm, pull and compare in its directory and logs every call to calls.
func stub(t *testing.T, perm, pull, compare string) (gh.Client, string) {
	t.Helper()
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "perm"), []byte(perm), 0o644)
	os.WriteFile(filepath.Join(d, "pull"), []byte(pull), 0o644)
	os.WriteFile(filepath.Join(d, "compare"), []byte(compare), 0o644)
	script := `#!/bin/sh
d="$(dirname "$0")"
printf '%s\n' "$*" >> "$d/calls"
case "$*" in
  *permission*) cat "$d/perm";;
  *compare*) cat "$d/compare";;
  *"/pulls/"*) cat "$d/pull";;
  *) echo '{}';;
esac
`
	os.WriteFile(filepath.Join(d, "gh"), []byte(script), 0o755)
	return gh.Client{Bin: filepath.Join(d, "gh")}, d
}

const pullOK = `{"state":"open","head":{"sha":"abc123","repo":{"full_name":"o/r"}},"base":{"ref":"main","sha":"def456"}}`

func run(t *testing.T, c gh.Client, d string, env map[string]string) (int, string, string) {
	t.Helper()
	out := filepath.Join(d, "out")
	os.WriteFile(out, nil, 0o644)
	base := map[string]string{"GITHUB_REPOSITORY": "o/r", "ACTOR": "u", "PR": "7", "COMMENT_ID": "99", "REQUIRE_UTD": "true", "GITHUB_OUTPUT": out}
	for k, v := range env {
		base[k] = v
	}
	var so, se bytes.Buffer
	rc := Parse(Env{Getenv: func(k string) string { return base[k] }, Stdout: &so, Stderr: &se, GH: c})
	o, _ := os.ReadFile(out)
	calls, _ := os.ReadFile(filepath.Join(d, "calls"))
	return rc, string(o), string(calls)
}

func TestAccepted(t *testing.T) {
	c, d := stub(t, `{"permission":"write"}`, pullOK, `{"behind_by":0}`)
	rc, out, calls := run(t, c, d, map[string]string{"BODY": "/tofu apply dev -target=a.b[\"x\"]"})
	if rc != 0 || !strings.Contains(out, "cmd=apply") || !strings.Contains(out, "targets=a.b[\"x\"]") || !strings.Contains(out, "names=dev") || strings.Contains(calls, "pr comment") {
		t.Fatalf("rc=%d out=%q calls=%q", rc, out, calls)
	}
}

func TestRejections(t *testing.T) {
	long := "/tofu " + strings.Repeat("é", 200) + "`"
	cases := []struct {
		name, body, perm, pull, cmp, want string
	}{
		{"upper case slash command", "/TOFU plan", "write", pullOK, "{}", "unknown command: plan"},
		{"empty target", "/tofu apply dev -target=", "write", pullOK, "{}", "unexpected argument: -target="},
		{"flag looking name", "/tofu plan -x", "write", pullOK, "{}", "unexpected argument: -x"},
		{"null head repo (deleted fork)", "/tofu plan", "write", `{"state":"open","head":{"sha":"abc123","repo":null},"base":{"ref":"main","sha":"def456"}}`, "{}", "fork PRs are not supported"},
		{"case differs is a fork", "/tofu plan", "write", strings.Replace(pullOK, `"o/r"`, `"O/r"`, 1), "{}", "fork PRs are not supported"},
		{"read access", "/tofu plan", "read", pullOK, "{}", "u lacks write access"},
		{"triage is not write", "/tofu plan", "triage", pullOK, "{}", "u lacks write access"},
		{"behind", "/tofu apply dev", "write", pullOK, `{"behind_by":2}`, "branch is 2 commits behind main"},
		{"angle brackets", "/tofu unlock a b", "write", pullOK, "{}", "usage: /tofu unlock &lt;workspace&gt;"},
		{"--now by a writer", "/tofu apply dev --now", "write", pullOK, "{}", "only repository admins can use --now (you have write access)"},
		{"--now with plan", "/tofu plan dev --now", "admin", pullOK, "{}", "--now only goes with /tofu apply"},
		{"unlock with ?", "/tofu unlock de?", "write", pullOK, "{}", "usage: /tofu unlock"},
		{"first line only, 120 characters", long, "write", pullOK, "{}", "❌ `/tofu " + strings.Repeat("é", 114) + "`: unknown command: " + strings.Repeat("é", 200)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, d := stub(t, `{"permission":"`+tc.perm+`"}`, tc.pull, tc.cmp)
			rc, out, calls := run(t, c, d, map[string]string{"BODY": tc.body})
			if rc != 1 || out != "" || !strings.Contains(calls, tc.want) || !strings.Contains(calls, "content=confused") {
				t.Fatalf("rc=%d out=%q calls=%q", rc, out, calls)
			}
		})
	}
}

func TestFailClosedWithoutReply(t *testing.T) {
	for name, tc := range map[string]struct{ perm, pull, cmp, body string }{
		"permission answer is not JSON": {"nope", pullOK, "{}", "/tofu plan"},
		"pull answer is not JSON":       {`{"permission":"write"}`, "nope", "{}", "/tofu plan"},
		"no usable sha":                 {`{"permission":"write"}`, `{"state":"open","head":{"sha":"x; rm","repo":{"full_name":"o/r"}},"base":{"ref":"main","sha":"def456"}}`, "{}", "/tofu plan"},
		"compare without behind_by":     {`{"permission":"write"}`, pullOK, `{}`, "/tofu apply dev"},
	} {
		t.Run(name, func(t *testing.T) {
			c, d := stub(t, tc.perm, tc.pull, tc.cmp)
			rc, out, _ := run(t, c, d, map[string]string{"BODY": tc.body})
			if rc != 1 || out != "" {
				t.Fatalf("rc=%d out=%q", rc, out)
			}
		})
	}
}

func TestMalformedEnvironmentMakesNoRequest(t *testing.T) {
	for k, v := range map[string]string{"ACTOR": "a/../b", "PR": "7x", "COMMENT_ID": "", "GITHUB_REPOSITORY": "o/r/x", "REQUIRE_UTD": "yes"} {
		c, d := stub(t, `{"permission":"admin"}`, pullOK, `{"behind_by":0}`)
		rc, _, calls := run(t, c, d, map[string]string{"BODY": "/tofu plan", k: v})
		if rc != 1 || calls != "" {
			t.Fatalf("%s=%q: rc=%d calls=%q", k, v, rc, calls)
		}
	}
}

func TestNowIsAnnouncedForAdmin(t *testing.T) {
	c, d := stub(t, `{"permission":"admin"}`, pullOK, `{"behind_by":0}`)
	rc, out, calls := run(t, c, d, map[string]string{"BODY": "/tofu apply dev --now"})
	if rc != 0 || !strings.Contains(out, "now=true") || !strings.Contains(calls, "⚡ **`--now`** used by @u:") {
		t.Fatalf("rc=%d out=%q calls=%q", rc, out, calls)
	}
}

func TestNoMatch(t *testing.T) {
	c, d := stub(t, "", "", "")
	env := map[string]string{"GITHUB_REPOSITORY": "o/r", "PR": "7", "COMMENT_ID": "99", "NAMES": "dev", "CMD": "apply"}
	var se bytes.Buffer
	e := Env{Getenv: func(k string) string { return env[k] }, Stderr: &se, GH: c}
	if NoMatch(e) != 0 {
		t.Fatal("rc")
	}
	calls, _ := os.ReadFile(filepath.Join(d, "calls"))
	if !strings.Contains(string(calls), "ℹ️ No workspace matches `dev`. Check the spelling;") {
		t.Fatalf("calls=%q", calls)
	}
	env["NAMES"] = "dev`; evil"
	if NoMatch(e) != 1 {
		t.Fatal("malformed names must stop")
	}
	env["NAMES"], env["CMD"] = "", "unlock"
	os.Remove(filepath.Join(d, "calls"))
	if NoMatch(e) != 0 {
		t.Fatal("rc")
	}
	calls, _ = os.ReadFile(filepath.Join(d, "calls"))
	if !strings.Contains(string(calls), "nothing to unlock. Name a workspace, for example `/tofu unlock <workspace>`.") {
		t.Fatalf("calls=%q", calls)
	}
}
