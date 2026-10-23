//go:build e2e

// Package e2e runs the real tfpls binary against real OpenTofu with a local backend and terraform_data resources: no cloud, no
// emulator, no network except the OpenTofu download of `tfpls setup`. GitHub is the fake `gh` of ./ghstub on the PATH.
// Run: go test -tags e2e ./internal/e2e/...  (TFPLS_E2E_TOFU=1 uses the tofu already on the PATH instead of installing one.)
package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	binDir       string // tfpls and gh
	toolPath     string // directories `tfpls setup` added to the PATH (tofu, conftest)
	haveConftest bool
	useHostTofu  bool
)

func TestMain(m *testing.M) { os.Exit(realMain(m)) }

func realMain(m *testing.M) int {
	tmp, err := os.MkdirTemp("", "tfpls-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp)
	binDir = filepath.Join(tmp, "bin")
	for _, b := range [][2]string{{"tfpls", "../../cmd/tfpls"}, {"gh", "./ghstub"}} {
		c := exec.Command("go", "build", "-o", filepath.Join(binDir, b[0]), b[1])
		if out, err := c.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "go build %s: %v\n%s", b[1], err, out)
			return 1
		}
	}
	useHostTofu = os.Getenv("TFPLS_E2E_TOFU") != ""
	if useHostTofu {
		if _, err := exec.LookPath("tofu"); err != nil {
			fmt.Fprintln(os.Stderr, "TFPLS_E2E_TOFU is set but there is no tofu on the PATH")
			return 1
		}
	} else if err := install(tmp, "true"); err != nil {
		// conftest is a nice-to-have: without it the policy scenario skips
		fmt.Fprintf(os.Stderr, "tfpls setup with conftest failed (%v); retrying OpenTofu alone\n", err)
		if err := install(tmp, "false"); err != nil {
			fmt.Fprintf(os.Stderr, "tfpls setup failed: %v\n", err)
			return 1
		}
	} else {
		haveConftest = true
	}
	if _, err := exec.LookPath("conftest"); err == nil && useHostTofu {
		haveConftest = true
	}
	// every process below (and the lookup of "tofu" itself) sees the tools `tfpls setup` installed first
	_ = os.Setenv("PATH", pathEnv())
	return m.Run()
}

// install runs `tfpls setup` the way a job does (the path comes back through $GITHUB_PATH).
func install(tmp, conftest string) error {
	gp := filepath.Join(tmp, "github_path")
	_ = os.Remove(gp)
	c := exec.Command(filepath.Join(binDir, "tfpls"), "setup")
	c.Env = append(os.Environ(), "GITHUB_PATH="+gp, "INSTALL_CONFTEST="+conftest, "GITHUB_WORKSPACE="+tmp)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	b, err := os.ReadFile(gp)
	if err != nil {
		return err
	}
	toolPath = strings.Join(strings.Fields(string(b)), string(os.PathListSeparator))
	return nil
}

func pathEnv() string {
	p := binDir
	if toolPath != "" {
		p += string(os.PathListSeparator) + toolPath
	}
	return p + string(os.PathListSeparator) + os.Getenv("PATH")
}

// repo is a git repository with a copy of a fixture in stack/ and its own stub directory.
type repo struct {
	t    *testing.T
	root string
	stub string
	tmp  string
	runs int
}

func copyDir(t *testing.T, from, to string) {
	t.Helper()
	ents, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(from, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo copies testdata/<fixture> to <repo>/stack and commits it.
func newRepo(t *testing.T, fixture string) *repo {
	t.Helper()
	tmp := t.TempDir()
	r := &repo{t: t, root: filepath.Join(tmp, "repo"), stub: filepath.Join(tmp, "stub"), tmp: tmp}
	copyDir(t, filepath.Join("testdata", fixture), filepath.Join(r.root, "stack"))
	for _, d := range []string{r.stub, filepath.Join(tmp, "runner")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git(t, r.root, "init", "-q")
	git(t, r.root, "add", "-A")
	git(t, r.root, "commit", "-q", "-m", "init")
	return r
}

func (r *repo) stack() string { return filepath.Join(r.root, "stack") }

// result is one invocation: exit code, console text, the step outputs and the comment posted on pull request 7.
type result struct {
	rc      int
	out     string
	outputs map[string]string
	comment string
}

var heredoc = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)<<(.+)$`)

func parseOutputs(s string) map[string]string {
	m := map[string]string{}
	lines := strings.Split(s, "\n")
	for i := 0; i < len(lines); i++ {
		if h := heredoc.FindStringSubmatch(lines[i]); h != nil {
			var body []string
			for i++; i < len(lines) && lines[i] != h[2]; i++ {
				body = append(body, lines[i])
			}
			m[h[1]] = strings.Join(body, "\n")
		} else if k, v, ok := strings.Cut(lines[i], "="); ok {
			m[k] = v
		}
	}
	return m
}

const entry = `{"name":"terraform/dev","label":"dev","dir":"stack","workspace":"","auto_apply":"never"}`

// tfpls runs `tfpls <args>` with the environment of a job step; kv overrides or adds KEY=value pairs.
func (r *repo) tfpls(args []string, kv ...string) result {
	r.t.Helper()
	r.runs++
	outFile := filepath.Join(r.tmp, fmt.Sprintf("gho-%d", r.runs))
	env := map[string]string{
		"PATH": pathEnv(), "HOME": r.tmp, "STUB_DIR": r.stub, "GITHUB_REPOSITORY": "o/r", "GITHUB_REPOSITORY_ID": "100",
		"DEFAULT_BRANCH": "main", "GITHUB_OUTPUT": outFile, "GITHUB_STEP_SUMMARY": filepath.Join(r.tmp, "summary"),
		"RUNNER_TEMP": filepath.Join(r.tmp, "runner"), "REPO_ROOT": r.root, "RUN_URL": "https://github.com/o/r/actions/runs/5",
		"GH_TOKEN": "x", "GITHUB_TOKEN": "x", "ENTRY": entry, "APPLY": "none", "PR": "7", "TF_IN_AUTOMATION": "1",
		"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_SYSTEM": "/dev/null",
	}
	for _, p := range kv {
		k, v, _ := strings.Cut(p, "=")
		env[k] = v
	}
	_ = os.Remove(filepath.Join(r.stub, "posted-7"))
	c := exec.Command(filepath.Join(binDir, "tfpls"), args...)
	for k, v := range env {
		c.Env = append(c.Env, k+"="+v)
	}
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	err := c.Run()
	res := result{out: buf.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		res.rc = ee.ExitCode()
	} else if err != nil {
		r.t.Fatalf("tfpls %v: %v", args, err)
	}
	b, _ := os.ReadFile(outFile)
	res.outputs = parseOutputs(string(b))
	cb, _ := os.ReadFile(filepath.Join(r.stub, "posted-7"))
	res.comment = string(cb)
	return res
}

// tofu runs the tofu on the PATH in the stack directory (a second process, or a look at the state).
func (r *repo) tofu(args ...string) (string, error) {
	c := exec.Command("tofu", args...)
	c.Dir = r.stack()
	c.Env = append(os.Environ(), "PATH="+pathEnv(), "TF_IN_AUTOMATION=1")
	out, err := c.CombinedOutput()
	return string(out), err
}

// stateAddresses lists the addresses in the local state, in a stable order (`tofu state list`).
func (r *repo) stateAddresses() string {
	r.t.Helper()
	out, err := r.tofu("state", "list", "-no-color")
	if strings.Contains(out, "No state file was found") {
		return ""
	}
	if err != nil {
		r.t.Fatalf("tofu state list: %v\n%s", err, out)
	}
	return strings.TrimSpace(out)
}

func eq(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s:\n got: %q\nwant: %q", what, got, want)
	}
}

func contains(t *testing.T, what, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Errorf("%s: %q not found in:\n%s", what, want, text)
	}
}
