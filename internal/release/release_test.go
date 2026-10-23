package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/o/r.git": "o/r",
		"git@github.com:o/r.git":     "o/r",
		"https://github.com/o/r":     "o/r",
		"o/r":                        "o/r",
		"":                           "",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSection(t *testing.T) {
	cl := "# Changelog\n\n## Unreleased\n\n- next\n\n## v1.2.3\n\n- a\n- b\n\n## v1.2.2\n\n- old\n"
	if got := Section(cl, "v1.2.3"); got != "- a\n- b" {
		t.Errorf("got %q", got)
	}
	if got := Section(cl, "v1.2"); got != "" { // exact heading match, no prefix surprise
		t.Errorf("prefix matched: %q", got)
	}
	if got := Section(cl, "v9.9.9"); got != "" {
		t.Errorf("missing section: %q", got)
	}
}

func TestTarballIsReproducibleSingleFile(t *testing.T) {
	a, err := tarball([]byte("binary"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := tarball([]byte("binary"))
	if !bytes.Equal(a, b) {
		t.Error("tarball is not reproducible")
	}
	zr, err := gzip.NewReader(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	h, err := tr.Next()
	if err != nil || h.Name != "tfpls" || h.Mode != 0o755 {
		t.Fatalf("header %+v err %v", h, err)
	}
	if d, _ := io.ReadAll(tr); string(d) != "binary" {
		t.Errorf("content %q", d)
	}
	if _, err := tr.Next(); err != io.EOF {
		t.Error("more than one entry")
	}
}

func TestBuildRejectsBadArguments(t *testing.T) {
	var out, errb bytes.Buffer
	env := Env{Stdout: &out, Stderr: &errb, Dir: t.TempDir()}
	get := func(string) string { return "" }
	if rc := Build(nil, env, get); rc != 2 {
		t.Errorf("no args: %d", rc)
	}
	if rc := Build([]string{"1.2.3", t.TempDir()}, env, get); rc != 2 {
		t.Errorf("tag without v: %d", rc)
	}
}

// A repository whose slug contains regexp/sed metacharacters (dots) must only rewrite its own references; a ref that
// merely starts with "main" (a branch named mainline) is not the main ref.
func TestReleaseDryRunRewritesOnlyOwnRefs(t *testing.T) {
	dir := t.TempDir()
	g := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		c.Dir = dir
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	wf := filepath.Join(dir, ".github/workflows")
	_ = os.MkdirAll(wf, 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "actions/bin"), 0o755)
	_ = os.WriteFile(filepath.Join(wf, "a.yml"), []byte("jobs:\n  x:\n    steps:\n      - uses: o.w/tfpls/actions/bin@main\n      - uses: oXw/tfpls/actions/bin@main\n      - uses: o.w/tfpls/actions/bin@mainline\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "actions/bin/VERSION"), []byte("dev\n"), 0o644)
	g("init", "-q")
	g("add", ".")
	g("commit", "-qm", "x")
	g("remote", "add", "origin", "https://github.com/o.w/tfpls.git")

	var out, errb bytes.Buffer
	if rc := Release([]string{"--dry-run", "v1.0.0"}, Env{Stdout: &out, Stderr: &errb, Dir: dir}); rc != 0 {
		t.Fatalf("rc %d: %s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "1 internal references now point at @v1.0.0 in 1 files") {
		t.Errorf("report: %s", out.String())
	}
	if !strings.Contains(out.String(), "dry run: nothing committed, tagged or pushed") {
		t.Errorf("report: %s", out.String())
	}
	if rc := Release([]string{"v1.0.0"}, Env{Stdout: &out, Stderr: &errb, Dir: dir}); rc != 1 {
		t.Errorf("a real run off main/with no origin main must fail, rc %d", rc)
	}
}
