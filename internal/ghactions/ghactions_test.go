package ghactions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newRunner(t *testing.T) (*Runner, *strings.Builder, string) {
	t.Helper()
	dir := t.TempDir()
	env := map[string]string{
		"GITHUB_OUTPUT":       filepath.Join(dir, "out"),
		"GITHUB_ENV":          filepath.Join(dir, "env"),
		"GITHUB_PATH":         filepath.Join(dir, "path"),
		"GITHUB_STEP_SUMMARY": filepath.Join(dir, "sum"),
	}
	var out strings.Builder
	return &Runner{Getenv: func(k string) string { return env[k] }, Stdout: &out}, &out, dir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSetOutputSingleLine(t *testing.T) {
	r, _, dir := newRunner(t)
	if err := r.SetOutput("a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := r.SetOutput("b", `{"x":"y"}`); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "out")); got != "a=1\nb={\"x\":\"y\"}\n" {
		t.Errorf("got %q", got)
	}
}

func TestSetOutputMultilineCannotForgeAnOutput(t *testing.T) {
	r, _, dir := newRunner(t)
	evil := "line1\nevil=1\nEOF"
	if err := r.SetOutput("body", evil); err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(dir, "out"))
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "body<<tfpls_") {
		t.Fatalf("not a heredoc: %q", got)
	}
	delim := strings.TrimPrefix(lines[0], "body<<")
	if lines[len(lines)-1] != delim || strings.Count(got, delim) != 2 {
		t.Errorf("delimiter must open and close the block exactly once: %q", got)
	}
}

func TestInvalidNamesRefused(t *testing.T) {
	r, _, _ := newRunner(t)
	for _, n := range []string{"", "a=b", "a\nb", "a\rb"} {
		if err := r.SetOutput(n, "v"); err == nil {
			t.Errorf("name %q accepted", n)
		}
		if err := r.SetEnv(n, "v"); err == nil {
			t.Errorf("env name %q accepted", n)
		}
	}
}

func TestNoFileIsNoOp(t *testing.T) {
	r := &Runner{Getenv: func(string) string { return "" }, Stdout: &strings.Builder{}}
	if err := r.SetOutput("a", "1"); err != nil {
		t.Error(err)
	}
	if err := r.AddPath("/x"); err != nil {
		t.Error(err)
	}
	if err := r.Summary("s"); err != nil {
		t.Error(err)
	}
}

func TestPathSummaryEnv(t *testing.T) {
	r, _, dir := newRunner(t)
	_ = r.AddPath("/opt/tfpls")
	_ = r.Summary("## hi")
	_ = r.SetEnv("K", "v")
	if got := read(t, filepath.Join(dir, "path")); got != "/opt/tfpls\n" {
		t.Errorf("path %q", got)
	}
	if got := read(t, filepath.Join(dir, "sum")); got != "## hi\n" {
		t.Errorf("summary %q", got)
	}
	if got := read(t, filepath.Join(dir, "env")); got != "K=v\n" {
		t.Errorf("env %q", got)
	}
}

func TestAnnotationsAreEscaped(t *testing.T) {
	r, out, _ := newRunner(t)
	r.Error("bad\n::error::forged 100%")
	r.Warning("w")
	r.Notice("n")
	want := "::error::bad%0A::error::forged 100%25\n::warning::w\n::notice::n\n"
	if out.String() != want {
		t.Errorf("got %q want %q", out.String(), want)
	}
}

func TestMask(t *testing.T) {
	r, out, _ := newRunner(t)
	r.Mask("a\r\n\nb")
	if out.String() != "::add-mask::a\n::add-mask::b\n" {
		t.Errorf("got %q", out.String())
	}
}
