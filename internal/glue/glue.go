// Package glue holds the small steps that used to be shell in the reusable workflows: the fork guard of plan.yml, the drift state
// of drift.yml, the merged-PR lookup of apply.yml, the drift issue lookup of drift-fix.yml and the dashboard page store of
// dashboard.yml. Inputs come from the environment as before; every value that reaches a path or an output is checked.
package glue

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/ghactions"
)

// Env is everything the package touches outside the process.
type Env struct {
	Getenv func(string) string
	Stdout io.Writer
	Stderr io.Writer
	GH     gh.Client
	Git    string // default "git"
	// Remote returns the URL to clone and push the data branch (default: https://github.com/<repo>.git); tests use a local path.
	Remote func(repo string) string
}

var (
	repoRE   = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	numberRE = regexp.MustCompile(`^[0-9]+$`)
	shaRE    = regexp.MustCompile(`^[0-9a-f]{6,64}$`)
	branchRE = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
)

// Main dispatches `tfpls glue <fork-check|drift-state|merged-pr|drift-issue|store-page>`.
func Main(args []string, e Env) int {
	if len(args) == 1 {
		r := &ghactions.Runner{Getenv: e.Getenv, Stdout: e.Stdout}
		switch args[0] {
		case "fork-check":
			return forkCheck(e, r)
		case "drift-state":
			return driftState(e, r)
		case "merged-pr":
			return mergedPR(e, r)
		case "drift-issue":
			return driftIssue(e, r)
		case "store-page":
			return storePage(e)
		}
	}
	fmt.Fprintln(e.Stderr, "usage: tfpls glue fork-check|drift-state|merged-pr|drift-issue|store-page")
	return 2
}

func die(e Env, f string, a ...any) int {
	fmt.Fprintf(e.Stderr, "tfpls glue: "+f+"\n", a...)
	return 1
}

// forkCheck (plan.yml): a pull request from a fork gets no secrets, so it is not planned. Env HEAD_REPO, GITHUB_REPOSITORY.
func forkCheck(e Env, r *ghactions.Runner) int {
	head, repo := e.Getenv("HEAD_REPO"), e.Getenv("GITHUB_REPOSITORY")
	if head != "" && head != repo {
		msg := "This pull request comes from a fork (" + head + "). Fork pull requests receive no credentials, so they are not planned. A maintainer can push the branch to " + repo + " to have it planned."
		r.SetOutput("is_fork", "true")
		r.NoticeTitle("Fork pull request not planned", msg)
		r.Summary("### Fork pull request not planned\n" + msg + "\n")
		return 0
	}
	r.SetOutput("is_fork", "false")
	return 0
}

// driftState (drift.yml): the drift state of a workspace from the status of the tofu step. Env S.
func driftState(e Env, r *ghactions.Runner) int {
	state := "error"
	switch e.Getenv("S") {
	case "no-changes", "applied":
		state = "in-sync"
	case "planned", "skipped":
		state = "drift"
	}
	r.SetOutput("state", state)
	return 0
}

// mergedPR (apply.yml): the pull request a commit was merged from. Outputs head_sha and pr_number (both empty for a direct push).
// Env GITHUB_REPOSITORY, SHA. A lookup that fails counts as a direct push, as before.
func mergedPR(e Env, r *ghactions.Runner) int {
	repo, sha := e.Getenv("GITHUB_REPOSITORY"), e.Getenv("SHA")
	if !repoRE.MatchString(repo) || !shaRE.MatchString(sha) {
		return die(e, "GITHUB_REPOSITORY and SHA must be set and well formed")
	}
	var pulls []struct {
		MergedAt *string `json:"merged_at"`
		Number   int     `json:"number"`
		Head     struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	head, num := "", ""
	if err := e.GH.JSON(&pulls, "api", "repos/"+repo+"/commits/"+sha+"/pulls"); err == nil {
		for _, p := range pulls {
			if p.MergedAt != nil && shaRE.MatchString(p.Head.SHA) && p.Number > 0 {
				head, num = p.Head.SHA, fmt.Sprint(p.Number)
				break
			}
		}
	}
	r.SetOutput("head_sha", head)
	r.SetOutput("pr_number", num)
	if num != "" {
		fmt.Fprintf(e.Stdout, "merged PR: #%s (head SHA %s)\n", num, head)
	} else {
		fmt.Fprintln(e.Stdout, "merged PR: none, direct push")
	}
	return 0
}

// driftIssue (drift-fix.yml): the issue that gets the plan comment: GIVEN, else the open `Drift: <label>` issue.
// Env GITHUB_REPOSITORY, GIVEN, LABEL. Output number.
func driftIssue(e Env, r *ghactions.Runner) int {
	repo, n, label := e.Getenv("GITHUB_REPOSITORY"), e.Getenv("GIVEN"), e.Getenv("LABEL")
	if !repoRE.MatchString(repo) || (n != "" && !numberRE.MatchString(n)) {
		return die(e, "GITHUB_REPOSITORY or GIVEN is malformed")
	}
	if n == "" {
		var issues []struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
		}
		if err := e.GH.JSON(&issues, "issue", "list", "--repo", repo, "--state", "open", "--label", "tfpls-drift", "--limit", "100", "--json", "number,title"); err == nil {
			for _, i := range issues {
				if i.Title == "Drift: "+label {
					n = fmt.Sprint(i.Number)
					break
				}
			}
		}
	}
	r.SetOutput("number", n)
	return 0
}

// storePage (dashboard.yml): commit SRC/index.html as dashboard/index.html on the data branch. Env GITHUB_REPOSITORY, TOKEN, BRANCH, SRC.
// The token travels in the environment of git (an extra header), never in a URL, argv or .git/config. Failures warn, as before.
func storePage(e Env) int {
	g := e.Getenv
	repo, token, branch, src := g("GITHUB_REPOSITORY"), g("TOKEN"), g("BRANCH"), g("SRC")
	if !repoRE.MatchString(repo) || !branchRE.MatchString(branch) || strings.HasPrefix(branch, "-") || token == "" || src == "" {
		return die(e, "GITHUB_REPOSITORY, TOKEN, BRANCH and SRC must be set and well formed")
	}
	warn := func(m string) { fmt.Fprintf(e.Stdout, "::warning::%s\n", m) }
	page, err := os.ReadFile(filepath.Join(src, "index.html"))
	if err != nil {
		return die(e, "%v", err)
	}
	dir, err := os.MkdirTemp("", "tfpls-page-")
	if err != nil {
		return die(e, "%v", err)
	}
	defer os.RemoveAll(dir)
	remote := "https://github.com/" + repo + ".git"
	if e.Remote != nil {
		remote = e.Remote(repo)
	}
	auth := "AUTHORIZATION: basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
	git := func(args ...string) error {
		bin := e.Git
		if bin == "" {
			bin = "git"
		}
		c := exec.Command(bin, args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader", "GIT_CONFIG_VALUE_0="+auth)
		return c.Run()
	}
	if err := git("clone", "--quiet", "--depth", "1", "--branch", branch, remote, dir); err != nil {
		warn("no `" + branch + "` branch to store the page on")
		return 0
	}
	if err := os.MkdirAll(filepath.Join(dir, "dashboard"), 0o755); err != nil {
		return die(e, "%v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dashboard", "index.html"), page, 0o644); err != nil {
		return die(e, "%v", err)
	}
	if git("add", "-A") != nil {
		return die(e, "git add failed")
	}
	if git("diff", "--cached", "--quiet") == nil {
		return 0 // nothing changed
	}
	if err := git("-c", "user.name=tfpls", "-c", "user.email=tfpls@users.noreply.github.com", "commit", "--quiet", "-m", "dashboard: refresh"); err != nil {
		return die(e, "git commit failed")
	}
	for i := 0; i < 3; i++ {
		if git("push", "--quiet", "origin", branch) == nil {
			return 0
		}
		git("-c", "user.name=tfpls", "-c", "user.email=tfpls@users.noreply.github.com", "pull", "--quiet", "--rebase", "origin", branch)
	}
	warn("could not store the page")
	return 0
}
