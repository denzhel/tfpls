package datastore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidBranch(t *testing.T) {
	for name, want := range map[string]bool{"tfpls-data": true, "a/b.c_d": true, "main": false, "master": false, "a b": false, "": false, "-x": false, "a..b": false, "a;b": false} {
		if got, _ := ValidBranch(name); got != want {
			t.Errorf("ValidBranch(%q) = %v", name, got)
		}
	}
}

func TestWriteCreatesBranchAndAppends(t *testing.T) {
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	s := Store{Branch: "tfpls-data", Remote: remote, Sleep: func(int) {}}
	for _, line := range []string{"one", "two"} {
		if err := s.Write("msg", func(d string) error {
			f, err := os.OpenFile(filepath.Join(d, "f"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.WriteString(line + "\n")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command("git", "--git-dir", remote, "show", "tfpls-data:f").Output()
	if err != nil || strings.TrimSpace(string(out)) != "one\ntwo" {
		t.Errorf("got %q %v", out, err)
	}
	// nothing to commit is a success
	if err := s.Write("msg", func(string) error { return nil }); err != nil {
		t.Error(err)
	}
}

func TestUnreachableRemoteFails(t *testing.T) {
	s := Store{Branch: "b", Remote: filepath.Join(t.TempDir(), "missing.git"), Attempts: 2, Sleep: func(int) {}}
	if err := s.Write("m", func(d string) error { return os.WriteFile(filepath.Join(d, "x"), []byte("x"), 0o644) }); err == nil {
		t.Error("want an error")
	}
}
