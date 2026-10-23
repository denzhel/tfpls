package detect

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denzhel/tfpls/internal/ghactions"
	"github.com/denzhel/tfpls/internal/yamlconf"
)

func problems(t *testing.T, yaml string) []Problem {
	t.Helper()
	cfg, err := yamlconf.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return Validate(cfg)
}

func hasMsg(ps []Problem, level, sub string) bool {
	for _, p := range ps {
		if p.Level == level && strings.Contains(p.Msg, sub) {
			return true
		}
	}
	return false
}

func TestValidateStrictAnchors(t *testing.T) {
	// BUG (the old shell version): `$` matched before a trailing newline, so "5m\n" and "SLACK\n" passed
	ps := problems(t, "defaults: { lock_timeout: \"5m\\n\", notify_secret: \"X\\n\" }\nstacks: [{ name: s, workspaces: [a] }]")
	if !hasMsg(ps, "error", "30s, 5m or 1h") || !hasMsg(ps, "error", "TFPLS_NOTIFY_WEBHOOKS") {
		t.Errorf("trailing newlines must be refused: %v", ps)
	}
	ps = problems(t, "stacks: [{ name: \"s\\n\", workspaces: [a] }]")
	if !hasMsg(ps, "error", "needs a `name`") {
		t.Errorf("a name with a newline must be refused: %v", ps)
	}
}

func TestValidateNonObjectConfig(t *testing.T) {
	if ps := Validate([]any{1}); !hasMsg(ps, "error", "is not a map") {
		t.Errorf("a list is not a config: %v", ps)
	}
	if ps := Validate(nil); len(ps) != 0 {
		t.Errorf("an empty file is valid: %v", ps)
	}
}

func TestValidatePolicyPacksElementsInterpolated(t *testing.T) {
	ps := problems(t, "defaults: { policy_packs: [5, Bad] }\nstacks: [{ name: s, workspaces: [a] }]")
	if !hasMsg(ps, "error", "got `5`") || !hasMsg(ps, "error", "got `Bad`") {
		t.Errorf("%v", ps)
	}
}

// the later of two matching override patterns wins: the document order is kept (a Go map would lose it)
func TestOverrideOrderIsDocumentOrder(t *testing.T) {
	dir := t.TempDir()
	cfg := "stacks:\n  - name: s\n    workspaces: [prod-a]\n    overrides:\n      \"prod-*\": { auto_apply: always }\n      \"*-a\": { auto_apply: additive_only }\n"
	out, code := runDetect(t, dir, cfg, nil, nil)
	if code != 0 || !strings.Contains(out["entries"], `"auto_apply":"additive_only"`) {
		t.Fatalf("code %d: %v", code, out)
	}
	cfg2 := "stacks:\n  - name: s\n    workspaces: [prod-a]\n    overrides:\n      \"*-a\": { auto_apply: additive_only }\n      \"prod-*\": { auto_apply: always }\n"
	out, _ = runDetect(t, dir, cfg2, nil, nil)
	if !strings.Contains(out["entries"], `"auto_apply":"always"`) {
		t.Errorf("%v", out)
	}
}

// runDetect runs Run in dir with the config and the environment; git is a fake that returns the given changed files.
func runDetect(t *testing.T, dir, cfg string, env map[string]string, git func(args ...string) ([]byte, error)) (map[string]string, int) {
	t.Helper()
	if cfg != "" {
		if err := os.WriteFile(filepath.Join(dir, ".tfpls.yml"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	outFile := filepath.Join(dir, "gho")
	_ = os.Remove(outFile)
	e := map[string]string{"CONFIG": ".tfpls.yml", "MODE": "changed", "GITHUB_OUTPUT": outFile}
	for k, v := range env {
		e[k] = v
	}
	getenv := func(k string) string { return e[k] }
	var stdout strings.Builder
	if git == nil {
		git = func(args ...string) ([]byte, error) { return nil, errors.New("no git") }
	}
	code := Run(Env{Getenv: getenv, Runner: &ghactions.Runner{Getenv: getenv, Stdout: &stdout}, Git: git})
	res := map[string]string{"stdout": stdout.String()}
	b, _ := os.ReadFile(outFile)
	for _, l := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			res[k] = v
		}
	}
	return res, code
}

func TestWorkspaceNameDotDotIsRefused(t *testing.T) {
	// BUG (bash): `..` matches [A-Za-z0-9_.-]+ and would reach paths and var files
	_, code := runDetect(t, t.TempDir(), "stacks: [{ name: s, workspaces: [\"..\"] }]", nil, nil)
	if code == 0 {
		t.Error("workspace .. must be refused")
	}
}

func TestChangedFilesAreNulSeparatedAndOptionsRefused(t *testing.T) {
	dir := t.TempDir()
	var diffArgs []string
	git := func(args ...string) ([]byte, error) {
		if args[0] == "cat-file" {
			return nil, nil
		}
		diffArgs = args
		// a path with a space and a quote: git would print it quoted without -z (BUG: it never matched a workspace)
		return []byte("workspaces/we ird\"/a.tf\x00"), nil
	}
	cfg := "stacks: [{ name: s, workspaces: [a], workspace_paths: [\"workspaces/{workspace}/\"] }]"
	out, code := runDetect(t, dir, cfg, map[string]string{"BASE": "abc123"}, git)
	if code != 0 || !strings.Contains(strings.Join(diffArgs, " "), "-z") {
		t.Fatalf("code %d args %v", code, diffArgs)
	}
	// the file belongs to no workspace: shared, so every workspace is selected
	if out["names"] != `["s/a"]` {
		t.Errorf("names %s", out["names"])
	}
	if _, code := runDetect(t, dir, cfg, map[string]string{"BASE": "--output=x"}, git); code == 0 {
		t.Error("an option as a base must be refused")
	}
}

func TestWorkspacesGlobSkipsDotDirsAndFiles(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"workspaces/dev", "workspaces/.hidden"} {
		_ = os.MkdirAll(filepath.Join(dir, d), 0o755)
	}
	_ = os.WriteFile(filepath.Join(dir, "workspaces", "file.txt"), nil, 0o644)
	out, code := runDetect(t, dir, "", map[string]string{"CONFIG": "none.yml", "MODE": "all"}, nil)
	if code != 0 || out["names"] != `["terraform/dev"]` {
		t.Errorf("code %d %v", code, out)
	}
}

func TestEntriesObjectKeepsListOrderAndNoHTMLEscaping(t *testing.T) {
	dir := t.TempDir()
	cfg := "stacks: [{ name: s, workspaces: [zz, aa], notify_mention: \"<!here> & co\" }]"
	out, _ := runDetect(t, dir, cfg, nil, nil)
	if !strings.HasPrefix(out["entries"], `{"zz":{`) || !strings.Contains(out["entries"], `"<!here> & co"`) {
		t.Errorf("%s", out["entries"])
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out["entries"]), &m); err != nil || len(m) != 2 {
		t.Errorf("entries is not valid JSON: %v", err)
	}
}

func TestCycleIsAnError(t *testing.T) {
	cfg := "stacks:\n  - name: s\n    workspaces: [a, b]\n    overrides:\n      a: { after: [b] }\n      b: { after: [a] }\n"
	out, code := runDetect(t, t.TempDir(), cfg, nil, nil)
	if code == 0 || !strings.Contains(out["stdout"], "cycle between workspaces: a, b") {
		t.Errorf("code %d %s", code, out["stdout"])
	}
}

func TestMissingWorkspacesFileFails(t *testing.T) {
	_, code := runDetect(t, t.TempDir(), "stacks: [{ name: s, workspaces_file: nope.txt }]", nil, nil)
	if code == 0 {
		t.Error("a missing workspaces_file must fail, not select nothing")
	}
}
