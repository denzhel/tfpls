package queue

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/denzhel/tfpls/internal/datastore"
	"github.com/denzhel/tfpls/internal/gh"
)

func TestBudget(t *testing.T) {
	for in, want := range map[string]int{"": 35, "60": 45, "20": 10, "50": 35, "31": 16, "30": 15, "4": 2, "2": 1, "1": 1, "0": 0, "abc": 35, "-5": 35, "5 ": 35} {
		if got := Budget(in); got != want {
			t.Errorf("Budget(%q) = %d, want %d", in, got, want)
		}
	}
}

// The Bash kept `..` as the queue directory name, which pointed outside queue/.
func TestSafeNameNeverAPathComponent(t *testing.T) {
	for in, want := range map[string]string{"a/b c": "a_b_c", "..": "_..", ".": "_.", "!!!": "_", "prod-eu.1": "prod-eu.1", "ü": "_"} {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// remote makes a bare repository with a data branch holding the given ticket files of queue q.
func remote(t *testing.T, q string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "remote.git")
	git(t, root, "init", "-q", "--bare", bare)
	w := filepath.Join(root, "w")
	os.MkdirAll(filepath.Join(w, "queue", q), 0o755)
	git(t, w, "init", "-q")
	git(t, w, "checkout", "-q", "-b", "tfpls-data")
	for n, c := range files {
		os.WriteFile(filepath.Join(w, "queue", q, n), []byte(c), 0o644)
	}
	os.WriteFile(filepath.Join(w, "README.md"), []byte("x"), 0o644)
	git(t, w, "add", "-A")
	git(t, w, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "x")
	git(t, w, "push", "-q", bare, "tfpls-data")
	return bare
}

func stubGH(t *testing.T, status string) gh.Client {
	dir := t.TempDir()
	bin := filepath.Join(dir, "gh")
	os.WriteFile(bin, []byte("#!/bin/sh\necho "+status+"\n"), 0o755)
	return gh.Client{Bin: bin}
}

func env(t *testing.T, bare string, kv map[string]string, client gh.Client) (Env, *bytes.Buffer) {
	var out bytes.Buffer
	kv["TFPLS_REMOTE_URL"] = bare
	kv["RUNNER_TEMP"] = t.TempDir()
	kv["REPO"] = "o/r"
	kv["TFPLS_RETRY_SLEEP"] = "0"
	g := func(k string) string { return kv[k] }
	now := time.Unix(1000, 0)
	return Env{Getenv: g, Stdout: &out, Stderr: &out, GH: client, Store: datastore.FromEnv(g), Pid: 7,
		Now: func() time.Time { return now }, Sleep: func(d time.Duration) { now = now.Add(2 * time.Minute) }}, &out
}

// An unreadable data branch while waiting must not read as an empty queue (the run would take the turn out of order).
func TestUnreadableQueueDoesNotGrantTheTurn(t *testing.T) {
	bare := remote(t, "ws", nil)
	e, out := env(t, bare, map[string]string{"MODE": "enter", "NAME": "ws", "RUN_ID": "5", "TIMEOUT": "4"}, stubGH(t, "in_progress"))
	// the first call takes the ticket; then the remote disappears
	real := e.Sleep
	e.Sleep = func(d time.Duration) { os.RemoveAll(bare); real(d) }
	// force a wait: a ticket of an active run is ahead
	// add an earlier ticket of another active run through a second queue entry
	e2, _ := env(t, bare, map[string]string{"MODE": "enter", "NAME": "ws", "RUN_ID": "4"}, stubGH(t, "completed"))
	if Run(e2) != 0 {
		t.Fatal("first enter")
	}
	if rc := Run(e); rc != 1 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	s := out.String()
	if strings.Contains(s, "our turn") || !strings.Contains(s, "could not read the queue") || !strings.Contains(s, "gave up waiting") {
		t.Fatal(s)
	}
}

func TestRunIDsFromTicketsAreOnlyNumbers(t *testing.T) {
	q := &queue{e: Env{GH: stubGH(t, "in_progress")}, repo: "o/r"}
	if q.runActive("../../x") || q.runActive("1; rm") || q.runActive("") {
		t.Fatal("a non-numeric run id was treated as active")
	}
	if !q.runActive("12") {
		t.Fatal("active run")
	}
}

func TestMissingModeAndUnknownMode(t *testing.T) {
	e, _ := env(t, t.TempDir(), map[string]string{"NAME": "x"}, gh.Client{})
	if Run(e) != 1 {
		t.Fatal("missing mode")
	}
	e, _ = env(t, t.TempDir(), map[string]string{"NAME": "x", "MODE": "dance"}, gh.Client{})
	if Run(e) != 2 {
		t.Fatal("unknown mode")
	}
}
