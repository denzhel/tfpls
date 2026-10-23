//go:build linux || darwin

package onboard

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// A real terminal under `curl | bash`: `cat onboard | bash -s -- args` runs in a pseudo-terminal, the program reads its
// answers from /dev/tty. (This is the part of tests/onboard.test.sh that used a Python pty helper.)

var (
	ptyBinOnce sync.Once
	ptyBin     string
	ptyBinErr  error
)

// tfplsBinary is the binary of cmd/tfpls: the one the shell tests share (TFPLS_TEST_BIN) or one built here.
func tfplsBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("TFPLS_TEST_BIN"); p != "" {
		return p
	}
	ptyBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "tfpls-pty")
		if err != nil {
			ptyBinErr = err
			return
		}
		ptyBin = filepath.Join(dir, "tfpls")
		out, err := exec.Command("go", "build", "-o", ptyBin, "github.com/denzhel/tfpls/cmd/tfpls").CombinedOutput()
		if err != nil {
			ptyBinErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if ptyBinErr != nil {
		t.Fatal(ptyBinErr)
	}
	return ptyBin
}

// pty runs the command in a pseudo-terminal (its controlling terminal), answers each "[Y/n]" or "[y/N]" question once
// from answers, and returns what the terminal showed; "KILLED (waiting for an answer)" is appended when the command
// had to be killed, "EXIT n" when it failed. deadline and grace: how long to let it run, how long to wait for it to
// exit after the terminal closed.
func pty(t *testing.T, answers []string, deadline, grace time.Duration, name string, args ...string) string {
	t.Helper()
	master, slave, err := openPty()
	if err != nil {
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	defer master.Close()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	slave.Close()

	var mu sync.Mutex
	var out bytes.Buffer
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		sent := 0
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				mu.Lock()
				out.Write(buf[:n])
				asked := bytes.Count(out.Bytes(), []byte("[Y/n]")) + bytes.Count(out.Bytes(), []byte("[y/N]"))
				for sent < len(answers) && sent < asked {
					master.Write([]byte(answers[sent] + "\n"))
					sent++
				}
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	var status error
	killed := false
	select {
	case status = <-waited:
	case <-time.After(deadline + grace):
		_ = cmd.Process.Kill()
		<-waited
		killed = true
	}
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
	}
	mu.Lock()
	defer mu.Unlock()
	s := out.String()
	if killed {
		s += "\nKILLED (waiting for an answer)"
	} else if ee, ok := status.(*exec.ExitError); ok {
		s += fmt.Sprintf("\nEXIT %d", ee.ExitCode())
	}
	return s
}

func TestRealTerminal(t *testing.T) {
	bin := tfplsBinary(t)
	tmp := t.TempDir()
	script := filepath.Join(tmp, "onboard")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec \""+bin+"\" onboard \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	curlBash := func(answers []string, args ...string) string {
		return pty(t, answers, 60*time.Second, 10*time.Second, "bash", append([]string{"-c", `cat "$0" | bash -s -- "$@"`, script}, args...)...)
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	t.Run("the question is read from the terminal and answered no", func(t *testing.T) {
		p := filepath.Join(tmp, "p")
		write(t, p, "workspaces/dev/.keep", "")
		out := curlBash([]string{"n"}, "--no-jokes", p)
		if !strings.Contains(out, "Write the new file(s)? [Y/n]") || exists(filepath.Join(p, ".github/workflows/tofu.yml")) || strings.Contains(out, "KILLED") {
			t.Errorf("under curl | bash in a terminal the question must be read from /dev/tty (answered no: nothing written):\n%s", out)
		}
	})
	t.Run("--yes in a terminal asks nothing and writes the files", func(t *testing.T) {
		p := filepath.Join(tmp, "p")
		out := curlBash(nil, "--yes", "--no-jokes", p)
		if !exists(filepath.Join(p, ".github/workflows/tofu.yml")) || strings.Contains(out, "[Y/n]") {
			t.Errorf("--yes in a terminal: no question, files written:\n%s", out)
		}
	})
	// the helper itself
	t.Run("helper: a child that closed its terminal and is still exiting is waited for", func(t *testing.T) {
		out := pty(t, nil, 60*time.Second, 10*time.Second, "bash", "-c", "exec >&- 2>&- </dev/null; sleep 0.4; exit 0")
		if strings.Contains(out, "KILLED") || strings.Contains(out, "EXIT") {
			t.Errorf("killed or failed: %q", out)
		}
	})
	t.Run("helper: a child that really hangs is killed and marked", func(t *testing.T) {
		out := pty(t, nil, time.Second, time.Second, "bash", "-c", "sleep 30")
		if !strings.Contains(out, "KILLED") {
			t.Errorf("a hanging child was not marked: %q", out)
		}
	})
	t.Run("helper: a failing child reports its exit status", func(t *testing.T) {
		out := pty(t, nil, 60*time.Second, 10*time.Second, "bash", "-c", "echo hi; exit 3")
		if !strings.Contains(out, "EXIT 3") {
			t.Errorf("exit status hidden: %q", out)
		}
	})
}
