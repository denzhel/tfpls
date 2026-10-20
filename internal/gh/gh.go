// Package gh runs the GitHub CLI as a process (authenticated by GH_TOKEN, which is the runner's token in a job).
// The modules need no API client of their own: `gh api`, `gh pr`, `gh run` already handle auth, hosts and pagination.
package gh

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Client runs gh. The zero value runs `gh` from the PATH.
type Client struct {
	Bin string   // default "gh"
	Env []string // extra KEY=VALUE for the process, on top of the current environment
}

// Error carries the failed command, its exit code and what gh said on stderr.
type Error struct {
	Args   []string
	Code   int
	Stderr string
}

func (e *Error) Error() string {
	return fmt.Sprintf("gh %s: exit %d: %s", strings.Join(e.Args, " "), e.Code, strings.TrimSpace(e.Stderr))
}

func (c Client) cmd(args []string, stdin io.Reader) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	bin := c.Bin
	if bin == "" {
		bin = "gh"
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin = stdin // nil = /dev/null: gh must never wait for a terminal
	if len(c.Env) > 0 {
		cmd.Env = append(cmd.Environ(), c.Env...)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	return cmd, &out, &errb
}

// Run returns what gh printed on stdout. A failure is an *Error.
func (c Client) Run(args ...string) (string, error) { return c.RunInput(nil, args...) }

// RunInput is Run with stdin (for example `gh api ... --input -`).
func (c Client) RunInput(stdin io.Reader, args ...string) (string, error) {
	cmd, out, errb := c.cmd(args, stdin)
	if err := cmd.Run(); err != nil {
		code := -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return out.String(), &Error{Args: args, Code: code, Stderr: errb.String() + errString(err, errb)}
	}
	return out.String(), nil
}

func errString(err error, errb *bytes.Buffer) string {
	if _, ok := err.(*exec.ExitError); ok || errb.Len() > 0 {
		return ""
	}
	return err.Error() // for example: gh not found
}

// JSON runs gh and decodes its stdout into v.
func (c Client) JSON(v any, args ...string) error {
	s, err := c.Run(args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(s), v); err != nil {
		return fmt.Errorf("gh %s: not JSON: %w", strings.Join(args, " "), err)
	}
	return nil
}

// Pages runs `gh api --paginate` and decodes the concatenated JSON documents it prints (one per page), calling fn for each.
func (c Client) Pages(fn func(dec *json.Decoder) error, args ...string) error {
	s, err := c.Run(append([]string{"api", "--paginate"}, args...)...)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(s))
	for dec.More() {
		if err := fn(dec); err != nil {
			return err
		}
	}
	return nil
}
