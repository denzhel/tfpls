package tofu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denzhel/tfpls/internal/ghactions"
)

// These are the cases of the former tests/run-save.test.sh (out() and oneline() of run.sh) and its opt-in loop.
func TestOutputsAreOneLine(t *testing.T) {
	f := filepath.Join(t.TempDir(), "gho")
	r := &runner{gha: &ghactions.Runner{Getenv: func(string) string { return f }, Stdout: &strings.Builder{}}}
	r.out("summary", "x\nstatus=applied")
	r.out("status", "error")
	r.out("summary", "a\r\nb")
	b, _ := os.ReadFile(f)
	if got := string(b); got != "summary=x status=applied\nstatus=error\nsummary=a  b\n" {
		t.Fatalf("outputs: %q", got)
	}
	if strings.Contains(oneline("ok\n::error::boom"), "\n") {
		t.Error("oneline left a line break")
	}
}

func TestUnencryptedWarns(t *testing.T) {
	for _, v := range []string{"", "false", "FALSE", "1", "yes"} {
		if !UnencryptedWarns(v) {
			t.Errorf("opt-in %q must keep the warning", v)
		}
	}
	if UnencryptedWarns("true") {
		t.Error("opt-in true silences the warning")
	}
}
