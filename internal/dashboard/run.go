package dashboard

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Env is injected for tests.
type Env struct {
	Getenv    func(string) string
	Stdout    io.Writer
	Stderr    io.Writer
	SetOutput func(name, value string) error
	Token     func() (string, error) // for `open`: the token of the gh login
	Open      func(path string)      // for `open`: show the page
}

var (
	branchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	repoRe   = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
)

func options(g func(string) string, repo string) (Options, error) {
	n, err := ParseEvents(g("EVENTS"))
	if err != nil {
		return Options{}, err
	}
	return Options{Repo: repo, ServerURL: g("SERVER_URL"), Events: n, Now: g("TFPLS_NOW")}, nil
}

// clone fetches a branch (depth 1) into a new directory with the token in the environment of git, never on a command line.
// A missing branch is not an error: ok is false and the directory is empty.
func clone(repo, branch, token string) (dir string, ok bool, err error) {
	if !repoRe.MatchString(repo) {
		return "", false, fmt.Errorf("invalid repository name")
	}
	if !branchRe.MatchString(branch) {
		return "", false, fmt.Errorf("invalid branch name")
	}
	dir, err = os.MkdirTemp("", "tfpls-dashboard-")
	if err != nil {
		return "", false, err
	}
	cmd := exec.Command("git", "clone", "--quiet", "--depth", "1", "--branch", branch, "https://github.com/"+repo+".git", dir)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if token != "" {
		auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader", "GIT_CONFIG_VALUE_0=AUTHORIZATION: basic "+auth)
	}
	if cmd.Run() != nil {
		os.RemoveAll(dir)
		dir, err = os.MkdirTemp("", "tfpls-dashboard-")
		return dir, false, err
	}
	return dir, true, nil
}

// Main implements `tfpls dashboard build|action|open`.
func Main(args []string, e Env) int {
	g := e.Getenv
	fail := func(f string, a ...any) int { fmt.Fprintf(e.Stderr, f+"\n", a...); return 1 }
	if len(args) == 0 {
		return fail("usage: tfpls dashboard build <data-dir> <out-dir> | action | open <owner/repo> [out-dir]")
	}
	switch args[0] {
	case "build": // env: REPO, SERVER_URL, EVENTS, TFPLS_NOW
		if len(args) != 3 {
			return fail("usage: tfpls dashboard build <data-dir> <out-dir>")
		}
		o, err := options(g, g("REPO"))
		if err != nil {
			return fail("%v", err)
		}
		w, ev, err := Build(args[1], args[2], o)
		if err != nil {
			return fail("dashboard: %v", err)
		}
		fmt.Fprintf(e.Stdout, "dashboard built: %d workspaces, %d events\n", w, ev)
	case "action": // env: BRANCH, OUT, EVENTS, TOKEN, REPO, SERVER_URL, WORKSPACE_DIR
		branch := g("BRANCH")
		if branch == "" {
			branch = "tfpls-data"
		}
		out := g("OUT")
		if out == "" || filepath.IsAbs(out) || strings.Contains(out, "..") {
			return fail("dashboard: out must be a relative directory inside the workspace")
		}
		o, err := options(g, g("REPO"))
		if err != nil {
			return fail("%v", err)
		}
		dir, ok, err := clone(g("REPO"), branch, g("TOKEN"))
		if err != nil {
			return fail("dashboard: %v", err)
		}
		defer os.RemoveAll(dir)
		if !ok {
			// a missing data branch is fine: the page then says nothing has been recorded yet
			fmt.Fprintf(e.Stdout, "::notice::branch `%s` does not exist yet (enable audit: true and run a drift check); building an empty dashboard\n", branch)
		}
		w, ev, err := Build(dir, filepath.Join(g("WORKSPACE_DIR"), out), o)
		if err != nil {
			return fail("dashboard: %v", err)
		}
		fmt.Fprintf(e.Stdout, "dashboard built: %d workspaces, %d events\n", w, ev)
		if err := e.SetOutput("path", out); err != nil {
			return fail("dashboard: %v", err)
		}
	case "open": // tools/tfpls-dashboard: the token is the gh login
		if len(args) < 2 || len(args) > 3 {
			return fail("usage: tfpls dashboard open <owner/repo> [out-dir]")
		}
		out := ""
		if len(args) == 3 {
			out = args[2]
		} else {
			d, err := os.MkdirTemp("", "tfpls-dashboard-out-")
			if err != nil {
				return fail("dashboard: %v", err)
			}
			out = d
		}
		token, err := e.Token()
		if err != nil {
			return fail("dashboard: could not read the gh login (gh auth token): %v", err)
		}
		dir, ok, err := clone(args[1], "tfpls-data", token)
		if err != nil {
			return fail("dashboard: %v", err)
		}
		defer os.RemoveAll(dir)
		if !ok {
			fmt.Fprintf(e.Stderr, "no tfpls-data branch in %s yet (enable audit: true and run a drift check); building an empty page\n", args[1])
		}
		o, err := options(g, args[1])
		if err != nil {
			return fail("%v", err)
		}
		if _, _, err := Build(dir, out, o); err != nil {
			return fail("dashboard: %v", err)
		}
		page := filepath.Join(out, "index.html")
		fmt.Fprintln(e.Stdout, page)
		if e.Open != nil {
			e.Open(page)
		}
	default:
		return fail("usage: tfpls dashboard build <data-dir> <out-dir> | action | open <owner/repo> [out-dir]")
	}
	return 0
}
