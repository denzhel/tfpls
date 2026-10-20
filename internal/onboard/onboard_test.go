package onboard

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// run onboards dir with the answers as the keyboard (nil: no terminal); NO_COLOR is set.
func run(t *testing.T, dir string, answers *string, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	env := Env{
		Getenv: func(k string) string { return map[string]string{"NO_COLOR": "1", "TFPLS_JOKE_SEED": "0"}[k] },
		Stdout: &out, Stderr: &out, Cwd: t.TempDir(),
	}
	if answers != nil {
		env.Input = strings.NewReader(*answers)
	}
	code := Run(append(args, dir), env)
	return code, out.String()
}

func str(s string) *string { return &s }

func scan(t *testing.T, dir string) *state {
	t.Helper()
	s := &state{root: dir, slug: "denzhel/tfpls", ref: "v0", topOK: true}
	s.scanGit()
	s.scanLayout()
	s.scanTF()
	s.scanGithubFiles()
	s.understand()
	return s
}

func TestLayoutFolders(t *testing.T) {
	d := t.TempDir()
	for _, n := range []string{"dev", "prod", "b ad", ".hidden"} {
		os.MkdirAll(filepath.Join(d, "workspaces", n), 0o755)
	}
	s := scan(t, d)
	if s.layout != "folders" || strings.Join(s.found, ",") != "dev,prod" || strings.Join(s.skipped, ",") != "b ad" {
		t.Fatalf("layout %q found %v skipped %v", s.layout, s.found, s.skipped)
	}
}

func TestLayoutVarFiles(t *testing.T) {
	d := t.TempDir()
	write(t, d, "main.tf", "terraform {\n  backend \"s3\" {}\n}\n")
	for _, f := range []string{"vars/dev.tfvars", "vars/prod.tfvars", "vars/terraform.tfvars", "vars/x.auto.tfvars"} {
		write(t, d, f, "")
	}
	s := scan(t, d)
	if s.layout != "varfile" || s.vardir != "vars" || strings.Join(s.varfiles, ",") != "dev,prod" {
		t.Fatalf("layout %q dir %q files %v", s.layout, s.vardir, s.varfiles)
	}
}

func TestLayoutEnvsAndRootStack(t *testing.T) {
	d := t.TempDir()
	for _, e := range []string{"envs/dev", "envs/prod", "modules/net", "workspaces2"} {
		write(t, d, e+"/main.tf", "terraform {\n  backend \"s3\" {}\n}\n")
	}
	write(t, d, "envs/dev/m.tf", "module \"n\" {\n  source = \"../../modules/net\"\n}\nmodule \"o\" {\n  source = \"../../../outside\"\n}\n")
	s := scan(t, d)
	// modules/ is never a stack; the root has no .tf: no root stack
	if s.layout != "envs" || strings.Join(s.envdirs, ",") != "envs/dev,envs/prod,workspaces2" || s.rootStack {
		t.Fatalf("layout %q envdirs %v rootStack %v", s.layout, s.envdirs, s.rootStack)
	}
	if got := s.moduleWatch("envs/dev"); strings.Join(got, ",") != "modules/net" {
		t.Fatalf("watch %v: a source outside the repository must be dropped", got)
	}
	write(t, d, "main.tf", "terraform {\n  backend \"s3\" {}\n}\n")
	s = scan(t, d)
	if !s.rootStack || strings.Join(s.stackDirs, ",") != ".,envs/dev,envs/prod,workspaces2" {
		t.Fatalf("root stack %v dirs %v", s.rootStack, s.stackDirs)
	}
}

func TestLayoutRootAndNone(t *testing.T) {
	d := t.TempDir()
	if s := scan(t, d); s.layout != "none" || len(s.stackDirs) != 0 {
		t.Fatalf("empty dir: %q", s.layout)
	}
	write(t, d, "a.tf", "resource \"aws_s3_bucket\" \"b\" {}\n")
	if s := scan(t, d); s.layout != "root" || !s.rootStack || s.stackName == "" {
		t.Fatalf("root: %q %v", s.layout, s.rootStack)
	}
}

func TestBackendBlocks(t *testing.T) {
	d := t.TempDir()
	write(t, d, "v.tf", `terraform {
  required_version = ">= 1.6"
  backend "s3" {
    region = "auto"
    endpoints = {
      s3 = "https://x.r2.cloudflarestorage.com"
    }
    use_lockfile = true
  }
}
provider "cloudflare" {}
resource "cloudflare_zone" "z" {}
resource "random_id" "r" {}
`)
	s := scan(t, d)
	if s.s3r2 != 1 || s.s3aws != 0 || s.lockMissing != 0 || strings.Join(s.providers, ",") != "cloudflare" {
		t.Fatalf("r2 %d aws %d lock %d providers %v", s.s3r2, s.s3aws, s.lockMissing, s.providers)
	}
	if s.needsAWS || s.ver != "" || !strings.Contains(s.verHint, "range (>= 1.6)") {
		t.Fatalf("needsAWS %v ver %q hint %q", s.needsAWS, s.ver, s.verHint)
	}
}

func TestBlocksStopAtTheClosingBrace(t *testing.T) {
	got := blocks(backendS3Re, [][]string{{"x", `backend "s3" {`, "  a = {", "  }", "}", "after"}, {`backend "s3" {}`, "tail"}})
	if strings.Join(got, "|") != `backend "s3" {|  a = {|  }|}|backend "s3" {}` {
		t.Fatalf("%q", got)
	}
}

func TestVersionSources(t *testing.T) {
	d := t.TempDir()
	write(t, d, "a.tf", "terraform {\n  required_version = \"= 1.11.4\"\n}\n")
	if s := scan(t, d); s.ver != "1.11.4" || s.verSrc != "required_version in a.tf" {
		t.Fatalf("%q %q", s.ver, s.verSrc)
	}
	write(t, d, ".opentofu-version", "# comment\nv1.12.0  # pinned\n")
	if s := scan(t, d); s.ver != "1.12.0" || s.verSrc != ".opentofu-version" {
		t.Fatalf("%q %q", s.ver, s.verSrc)
	}
}

func TestParseArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		code int
	}{
		{[]string{"--nope"}, 2},
		{[]string{"--ref"}, 2},
		{[]string{"--ref", "bad ref"}, 2},
		{[]string{"--slug", "nope"}, 2},
		{[]string{"--runs-on", "x y"}, 2},
		{[]string{"a", "b"}, 2},
		{[]string{"--yes", "dir", "--yes"}, 2}, // options end at the first directory, as before
		{[]string{"--help"}, 0},
		{[]string{"/definitely/not/here"}, 1},
	} {
		var out bytes.Buffer
		if got := Run(c.args, Env{Stdout: &out, Stderr: &out, Cwd: t.TempDir()}); got != c.code {
			t.Errorf("%v: exit %d, want %d (%s)", c.args, got, c.code, out.String())
		}
	}
}

func TestIdempotentAndDiff(t *testing.T) {
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "workspaces", "dev"), 0o755)
	if code, out := run(t, d, nil); code != 0 || !strings.Contains(out, "(written)") {
		t.Fatalf("first run %d: %s", code, out)
	}
	first, _ := os.ReadFile(filepath.Join(d, ".github/workflows/tofu.yml"))
	if code, out := run(t, d, nil); code != 0 || !strings.Contains(out, "already up to date") {
		t.Fatalf("second run %d: %s", code, out)
	}
	// a differing workflow is shown as a diff and kept without a yes
	code, out := run(t, d, nil, "--runs-on", "ubuntu-slim")
	got, _ := os.ReadFile(filepath.Join(d, ".github/workflows/tofu.yml"))
	if code != 0 || string(got) != string(first) || !strings.Contains(out, "+    with: { runs_on: ubuntu-slim }") ||
		!strings.Contains(out, ".github/workflows/tofu.yml (yours)") || !strings.Contains(out, "kept your") {
		t.Fatalf("differing run %d: %s", code, out)
	}
	// a yes replaces it
	if _, out := run(t, d, str("y\n"), "--runs-on", "ubuntu-slim"); !strings.Contains(out, "Replace .github/workflows/tofu.yml with the recommended caller? [y/N] y") {
		t.Fatalf("question not shown: %s", out)
	}
	got, _ = os.ReadFile(filepath.Join(d, ".github/workflows/tofu.yml"))
	if !strings.Contains(string(got), "runs_on: ubuntu-slim") {
		t.Fatalf("not replaced:\n%s", got)
	}
	// --force replaces; an existing .tfpls.yml is never overwritten
	write(t, d, ".tfpls.yml", "overrides: {}\n")
	run(t, d, nil, "--force")
	if c, _ := os.ReadFile(filepath.Join(d, ".tfpls.yml")); string(c) != "overrides: {}\n" {
		t.Fatalf(".tfpls.yml overwritten: %s", c)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	d := t.TempDir()
	write(t, d, "main.tf", "terraform {\n  backend \"s3\" {}\n}\n")
	code, out := run(t, d, nil, "--dry-run")
	if code != 0 || !strings.Contains(out, "dry run: nothing was written") || !strings.Contains(out, "+name: tofu") {
		t.Fatalf("%d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(d, ".github")); err == nil {
		t.Fatal("dry run wrote .github")
	}
	if _, err := os.Stat(filepath.Join(d, ".tfpls.yml")); err == nil {
		t.Fatal("dry run wrote .tfpls.yml")
	}
}

func TestPrompts(t *testing.T) {
	d := t.TempDir()
	write(t, d, "a.tf", "resource \"cloudflare_zone\" \"z\" {}\n")
	// the stack name, then no to the files
	code, out := run(t, d, str("web edge\nweb-edge\nn\n"))
	if code != 0 || !strings.Contains(out, "that does not look right, try again") ||
		!strings.Contains(out, "Name of the stack (shown in comments, used by /tofu apply <name>) [") ||
		!strings.Contains(out, "Write the new file(s)? [Y/n] n") {
		t.Fatalf("%d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(d, ".github")); err == nil {
		t.Fatal("answered no, but files were written")
	}
	// an answer that is neither y nor n is asked again, three times, then the default counts
	_, out = run(t, d, str("web-edge\nmaybe\nmaybe\nmaybe\n"))
	if strings.Count(out, "please answer y or n") != 3 {
		t.Fatalf("%s", out)
	}
	if _, err := os.Stat(filepath.Join(d, ".github/workflows/tofu.yml")); err != nil {
		t.Fatal("the default (yes) was not taken after three bad answers")
	}
	// the end of the input takes the defaults
	d2 := t.TempDir()
	write(t, d2, "a.tf", "resource \"aws_s3_bucket\" \"b\" {}\n")
	if code, _ := run(t, d2, str("")); code != 0 {
		t.Fatal("end of input must take the defaults")
	}
}

func TestNoQuestionsWithYes(t *testing.T) {
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "workspaces", "dev"), 0o755)
	_, out := run(t, d, str("n\n"), "--yes")
	if strings.Contains(out, "[Y/n]") || !strings.Contains(out, "--yes: no questions") {
		t.Fatal(out)
	}
}

func TestJokePicker(t *testing.T) {
	if len(jokes) < 20 {
		t.Fatal("fewer than 20 jokes")
	}
	seen := map[int]bool{}
	for i := 0; i < len(jokes); i++ {
		seen[pickJoke(strconv.Itoa(i))] = true
	}
	if len(seen) != len(jokes) {
		t.Fatalf("%d seeds gave %d jokes", len(jokes), len(seen))
	}
	if pickJoke("7") != pickJoke("7") || pickJoke("007") != 7%len(jokes) || pickJoke("123456789") != 123456789%len(jokes) {
		t.Fatal("a seed must pick the same joke, in decimal (the Bash picker used 10#)")
	}
	for _, bad := range []string{"", "x", "1234567890", "-1"} {
		if i := pickJoke(bad); i < 0 || i >= len(jokes) {
			t.Fatalf("seed %q: %d", bad, i)
		}
	}
}

func TestUnifiedDiff(t *testing.T) {
	got := strings.Join(unifiedDiff("a", "b", "1\n2\n3\n4\n5\n6\n7\n8\n9\n", "1\n2\n3\n4\nX\n6\n7\n8\n9\n"), "\n")
	want := "--- a\n+++ b\n@@ -2,7 +2,7 @@\n 2\n 3\n 4\n-5\n+X\n 6\n 7\n 8"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if d := unifiedDiff("a", "b", "x\n", "x\n"); d != nil {
		t.Fatal("equal files give no diff")
	}
	if got := strings.Join(unifiedDiff("/dev/null", "n", "", "a\nb\n"), "\n"); got != "--- /dev/null\n+++ n\n@@ -0,0 +1,2 @@\n+a\n+b" {
		t.Fatalf("new file:\n%s", got)
	}
	if got := strings.Join(unifiedDiff("a", "b", "x", "x\n"), "\n"); !strings.Contains(got, `\ No newline at end of file`) {
		t.Fatalf("missing newline marker:\n%s", got)
	}
}

func TestWidthCountsCharacters(t *testing.T) {
	var out bytes.Buffer
	s := &state{out: &out}
	s.fileline("", "+", "wörkflow-é.yml", "x")
	if !strings.Contains(out.String(), "wörkflow-é.yml"+strings.Repeat(" ", 28-len([]rune("wörkflow-é.yml")))+" ") {
		t.Fatalf("%q", out.String())
	}
}
