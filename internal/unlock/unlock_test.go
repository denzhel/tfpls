package unlock

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const locked = `
Error: Error acquiring the state lock

Error message: operation error S3: PutObject, https response error StatusCode: 412
Lock Info:
  ID:        138e6af8-18fc-5526-e4fa-40efcf6236f5
  Path:      example-state-bucket/stg-us-west-2/workspace.tf.state
  Operation: OperationTypeApply
  Who:       ABCDE@ip-192-168-75-4.ec2.internal
  Version:   1.10.6
  Created:   2021-10-31 09:27:42.742425 +0000 UTC
  Info:

OpenTofu acquires a state lock to protect the state from being written
by multiple users at the same time.`

func TestParseLock(t *testing.T) {
	l, rc := ParseLock(locked)
	if rc != 0 || l.ID != "138e6af8-18fc-5526-e4fa-40efcf6236f5" || l.Who != "ABCDE@ip-192-168-75-4.ec2.internal" ||
		l.Op != "OperationTypeApply" || l.Created != "2021-10-31 09:27:42.742425 +0000 UTC" {
		t.Fatalf("%d %+v", rc, l)
	}
	if _, rc := ParseLock("No changes. Your infrastructure matches the configuration."); rc != 1 {
		t.Error("no lock error => 1")
	}
	if _, rc := ParseLock("Error: Error acquiring the state lock\nLock Info:\n  Who: someone"); rc != 2 {
		t.Error("no ID => 2")
	}
	if _, rc := ParseLock("Error: Error acquiring the state lock\nLock Info:\n  ID: x; rm -rf /"); rc != 2 {
		t.Error("unsafe ID => 2")
	}
}

func TestAgeMinutes(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]int{"2026-10-09 10:30:00.1 +0000 UTC": 90, "2026-10-09 11:57:00 +0000 UTC": 3} {
		if got, ok := AgeMinutes(in, now); !ok || got != want {
			t.Errorf("%q: %d %v", in, got, ok)
		}
	}
	// GNU date accepted words such as these; a lock age must come from a plain date only
	for _, in := range []string{"yesterday-ish", "tomorrow 00:00:00 ", "", "2026-13-45 99:99:99"} {
		if _, ok := AgeMinutes(in, now); ok {
			t.Errorf("%q accepted", in)
		}
	}
}

// tofu runs without the GitHub token and the plan key; the cloud credentials stay.
func TestTofuGetsNoGitHubTokenAndNoPlanKey(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tofu"), []byte("#!/bin/sh\nenv | grep -E '^(GH_TOKEN|GITHUB_TOKEN|PLAN_ENCRYPTION_KEY|AWS_ACCESS_KEY_ID|EXTRA)=' | sort\n"), 0o755)
	e := Env{Tofu: filepath.Join(dir, "tofu"), Environ: func() []string {
		return []string{"GH_TOKEN=s1", "GITHUB_TOKEN=s2", "PLAN_ENCRYPTION_KEY=s3", "AWS_ACCESS_KEY_ID=AKIA", "PATH=" + os.Getenv("PATH")}
	}}
	out, err := e.tofu(context.Background(), dir, []string{"EXTRA=1"}, "version")
	if err != nil || strings.ReplaceAll(out, "\n", " ") != "AWS_ACCESS_KEY_ID=AKIA EXTRA=1 " {
		t.Fatalf("%v %q", err, out)
	}
}

func TestCleanupRemovesOnlyItsFilesAndToleratesEmptyVariables(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, "stack")
	os.MkdirAll(filepath.Join(d, ".terraform", "providers"), 0o755)
	for _, f := range []string{"init.txt", "unlock.txt", "main.tf", "tfplan"} {
		os.WriteFile(filepath.Join(d, f), []byte("x"), 0o644)
	}
	cleanup(root, "stack")
	ents, _ := os.ReadDir(d)
	var got []string
	for _, en := range ents {
		got = append(got, en.Name())
	}
	if strings.Join(got, ",") != "main.tf,tfplan" {
		t.Fatal(got)
	}
	cleanup("", "")
	cleanup(root, "")
	cleanup(root, "missing")
}

func TestDirectoryMustStayInTheWorkspace(t *testing.T) {
	var out bytes.Buffer
	env := map[string]string{"REPO_ROOT": t.TempDir(), "DIR": "../..", "NAME": "x"}
	e := Env{Getenv: func(k string) string { return env[k] }, Environ: func() []string { return nil }, Stdout: &out}
	if Run(e) != 1 || !strings.Contains(out.String(), "not found") {
		t.Fatal(out.String())
	}
}

// The Bash sweep read an unreadable ENTRIES as "no workspaces" and finished green.
func TestSweepWithUnreadableEntriesFails(t *testing.T) {
	for _, in := range []string{"{not json", "[1]", "null"} {
		var out bytes.Buffer
		env := map[string]string{"ENTRIES": in}
		e := Env{Getenv: func(k string) string { return env[k] }, Environ: func() []string { return nil }, Stdout: &out}
		if Run(e) != 1 {
			t.Errorf("%q: %s", in, out.String())
		}
	}
}
