// Package ghactions talks to the Actions runner: step outputs, environment, PATH, the step summary,
// annotations and secret masking. Every function is a no-op for a file the runner did not provide
// (outside a job, in tests without the variable), the way `>> "${GITHUB_OUTPUT:-/dev/null}"` was.
package ghactions

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// Runner is the connection to one step of a job.
type Runner struct {
	Getenv func(string) string
	Stdout io.Writer // workflow commands (::error:: ...) go here
}

// New returns a Runner for the real process environment.
func New() *Runner { return &Runner{Getenv: os.Getenv, Stdout: os.Stdout} }

func (r *Runner) appendTo(envVar, text string) error {
	path := r.Getenv(envVar)
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// keyValue renders `name=value`, or the heredoc form when the value has a line break. The delimiter is random and is
// guaranteed not to occur in the value, so a value can never end its own block and forge a second output or variable.
func keyValue(name, value string) (string, error) { return keyValueForce(name, value, false) }

// keyValueForce is keyValue that can insist on the heredoc form even for a one-line value.
func keyValueForce(name, value string, heredoc bool) (string, error) {
	if name == "" || strings.ContainsAny(name, "=\r\n") {
		return "", fmt.Errorf("invalid name %q", name)
	}
	if !heredoc && !strings.ContainsAny(value, "\r\n") {
		return name + "=" + value + "\n", nil
	}
	for {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		d := "tfpls_" + hex.EncodeToString(b)
		if !strings.Contains(value, d) {
			return name + "<<" + d + "\n" + value + "\n" + d + "\n", nil
		}
	}
}

// SetOutput sets a step output ($GITHUB_OUTPUT).
func (r *Runner) SetOutput(name, value string) error {
	return r.keyValueTo("GITHUB_OUTPUT", name, value)
}

// SetEnv sets an environment variable for the following steps ($GITHUB_ENV).
func (r *Runner) SetEnv(name, value string) error { return r.keyValueTo("GITHUB_ENV", name, value) }

// SetEnvBlock is SetEnv in the heredoc form for every value (what a consumer that reads the file line by line expects).
func (r *Runner) SetEnvBlock(name, value string) error {
	s, err := keyValueForce(name, value, true)
	if err != nil {
		return err
	}
	return r.appendTo("GITHUB_ENV", s)
}

func (r *Runner) keyValueTo(envVar, name, value string) error {
	s, err := keyValue(name, value)
	if err != nil {
		return err
	}
	return r.appendTo(envVar, s)
}

// AddPath puts a directory on the PATH of the following steps ($GITHUB_PATH).
func (r *Runner) AddPath(dir string) error { return r.appendTo("GITHUB_PATH", dir+"\n") }

// Summary appends markdown to the job summary ($GITHUB_STEP_SUMMARY).
func (r *Runner) Summary(markdown string) error {
	if !strings.HasSuffix(markdown, "\n") {
		markdown += "\n"
	}
	return r.appendTo("GITHUB_STEP_SUMMARY", markdown)
}

// escape makes a message safe for a workflow command: a line break would end the command and let the rest of the text
// become a command of its own.
func escape(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func (r *Runner) command(kind, msg string) { fmt.Fprintf(r.Stdout, "::%s::%s\n", kind, escape(msg)) }

// Error, Warning and Notice write an annotation.
func (r *Runner) Error(msg string)   { r.command("error", msg) }
func (r *Runner) Warning(msg string) { r.command("warning", msg) }
func (r *Runner) Notice(msg string)  { r.command("notice", msg) }

// NoticeTitle is Notice with a title (property values escape : and , as well).
func (r *Runner) NoticeTitle(title, msg string) {
	t := strings.NewReplacer(":", "%3A", ",", "%2C").Replace(escape(title))
	fmt.Fprintf(r.Stdout, "::notice title=%s::%s\n", t, escape(msg))
}

// Mask hides every line of a secret in the logs of the following steps. Empty lines are skipped (an empty mask would hide nothing
// and some runners reject it).
func (r *Runner) Mask(secret string) {
	for _, line := range strings.Split(strings.ReplaceAll(secret, "\r", ""), "\n") {
		if line != "" {
			r.command("add-mask", line)
		}
	}
}
