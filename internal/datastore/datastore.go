// Package datastore changes files on the data branch (an orphan branch, created on first use) and pushes, starting over
// from the new tip when another run won the race. Shared by record, finish and drift-report.
package datastore

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Store describes one data branch.
type Store struct {
	Branch string // default tfpls-data
	Repo   string // owner/name, for the GitHub remote
	Token  string // sent as an HTTP header through the environment of git, never in an argument list or URL
	Remote string // test override (TFPLS_REMOTE_URL): a path or URL used as is, without a token
	// Sleep pauses between attempts; nil = a growing random pause (parallel drift legs and applies share ONE ref).
	Sleep    func(attempt int)
	Attempts int // default 10
}

// FromEnv reads BRANCH, REPO, TOKEN, TFPLS_REMOTE_URL and TFPLS_RETRY_SLEEP (seconds, may be fractional; tests).
func FromEnv(getenv func(string) string) Store {
	s := Store{Branch: getenv("BRANCH"), Repo: getenv("REPO"), Token: getenv("TOKEN"), Remote: getenv("TFPLS_REMOTE_URL")}
	if s.Branch == "" {
		s.Branch = "tfpls-data"
	}
	if v := getenv("TFPLS_RETRY_SLEEP"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			s.Sleep = func(int) { time.Sleep(time.Duration(f * float64(time.Second))) }
		}
	}
	return s
}

func (s Store) git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if s.Remote == "" && s.Token != "" {
		auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + s.Token))
		env = append(env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader", "GIT_CONFIG_VALUE_0=AUTHORIZATION: basic "+auth)
	}
	cmd.Env = env
	return cmd.Run()
}

func (s Store) remote() string {
	if s.Remote != "" {
		return s.Remote
	}
	return "https://github.com/" + s.Repo + ".git"
}

// Write clones the branch (or starts it), lets writer edit the files in the clone, commits and pushes. Nothing to commit
// is a success. It returns an error when it could not be done after all attempts.
func (s Store) Write(msg string, writer func(dir string) error) error {
	n := s.Attempts
	if n == 0 {
		n = 10
	}
	for attempt := 1; attempt <= n; attempt++ {
		d, err := os.MkdirTemp("", "tfpls-data-")
		if err != nil {
			return err
		}
		done, err := s.once(d, msg, writer)
		os.RemoveAll(d)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if attempt < n {
			if s.Sleep != nil {
				s.Sleep(attempt)
			} else {
				time.Sleep(time.Duration(1+rand.Intn(3+2*attempt)) * time.Second)
			}
		}
	}
	return fmt.Errorf("could not push %s after %d attempts", s.Branch, n)
}

// once returns done=true when pushed (or nothing changed), done=false when the push lost a race, an error when it is hopeless.
func (s Store) once(d, msg string, writer func(string) error) (bool, error) {
	if err := s.git("", "clone", "--quiet", "--depth", "1", "--branch", s.Branch, s.remote(), d); err != nil {
		os.RemoveAll(d)
		if err := os.MkdirAll(d, 0o700); err != nil {
			return false, err
		}
		if err := s.git(d, "init", "--quiet"); err != nil {
			return false, err
		}
		if err := s.git(d, "checkout", "--quiet", "-b", s.Branch); err != nil {
			return false, err
		}
		if err := s.git(d, "remote", "add", "origin", s.remote()); err != nil {
			return false, err
		}
	}
	if err := writer(d); err != nil {
		return false, err
	}
	if err := s.git(d, "add", "-A"); err != nil {
		return false, err
	}
	if s.git(d, "diff", "--cached", "--quiet") == nil {
		return true, nil
	}
	if err := s.git(d, "-c", "user.name=tfpls", "-c", "user.email=tfpls@users.noreply.github.com", "commit", "--quiet", "-m", msg); err != nil {
		return false, err
	}
	if s.git(d, "push", "--quiet", "origin", s.Branch) == nil {
		return true, nil
	}
	return false, nil
}

// ValidBranch says whether name is acceptable as the data branch: plain characters, not main or master, and not
// something git could read as an option or a parent path.
func ValidBranch(name string) (bool, string) {
	if name == "main" || name == "master" {
		return false, "refusing to write to `" + name + "`"
	}
	if name == "" || strings.HasPrefix(name, "-") || strings.Contains(name, "..") {
		return false, "invalid branch name"
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '/' || r == '-') {
			return false, "invalid branch name"
		}
	}
	return true, ""
}
