// Package prcomments holds the two small pull request comment jobs: the sticky first-run notes (pr-notes) and the "Outdated" banner on
// plan comments of workspaces the latest commit does not touch (mark-outdated). Both are best effort: they never fail the job.
package prcomments

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/denzhel/tfpls/internal/gh"
)

// Env is everything the jobs read from the outside, injected for tests.
type Env struct {
	Getenv func(string) string
	Stdout io.Writer
	Stderr io.Writer
	GH     gh.Client
	Output func(name, value string) error // writes a step output
	Git    func(args ...string) bool      // runs git in the current directory, true when it succeeded
}

// GitSuccess runs git with an argument list (no shell) and reports whether it succeeded.
func GitSuccess(args ...string) bool { return exec.Command("git", args...).Run() == nil }

type comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User *struct {
		Type string `json:"type"`
	} `json:"user"`
}

func (c comment) isBot() bool { return c.User != nil && c.User.Type == "Bot" }

func listComments(c gh.Client, repo, pr string) ([]comment, error) {
	var all []comment
	err := c.Pages(func(dec *json.Decoder) error {
		var page []comment
		if err := dec.Decode(&page); err != nil {
			return err
		}
		all = append(all, page...)
		return nil
	}, fmt.Sprintf("repos/%s/issues/%s/comments?per_page=100", repo, pr))
	return all, err
}

func patch(c gh.Client, repo string, id int64, body string) error {
	_, err := c.Run("api", "-X", "PATCH", fmt.Sprintf("repos/%s/issues/comments/%d", repo, id), "-f", "body="+body)
	return err
}

// Notes implements `tfpls pr-notes <mode>`.
func Notes(args []string, e Env) int {
	if len(args) < 1 || args[0] == "" {
		fmt.Fprintln(e.Stderr, "usage: tfpls pr-notes introduced|no-workspaces|clear")
		return 2
	}
	repo, pr := e.Getenv("REPO"), e.Getenv("PR")
	docs := "https://github.com/" + orDefault(e.Getenv("GITHUB_ACTION_REPOSITORY"), "denzhel/tfpls") + "/blob/main"
	upsert := func(marker, body string) {
		if pr == "" || repo == "" {
			return
		}
		full := marker + "\n" + body
		cs, err := listComments(e.GH, repo, pr)
		if err != nil {
			// Listing failed: posting now could duplicate the sticky note, so do nothing (best effort).
			return
		}
		for _, c := range cs {
			if c.isBot() && strings.HasPrefix(c.Body, marker) {
				_ = patch(e.GH, repo, c.ID, full)
				return
			}
		}
		_, _ = e.GH.Run("api", fmt.Sprintf("repos/%s/issues/%s/comments", repo, pr), "-f", "body="+full)
	}
	switch args[0] {
	case "introduced":
		introduced := false
		path := strings.TrimPrefix(e.Getenv("WF_REF"), repo+"/")
		if i := strings.Index(path, "@"); i >= 0 {
			path = path[:i]
		}
		base := e.Getenv("BASE_SHA")
		if base != "" && path != "" && e.Git("cat-file", "-e", base+"^{commit}") && !e.Git("cat-file", "-e", base+":"+path) {
			introduced = true
		}
		if e.Output != nil {
			_ = e.Output("introduced", fmt.Sprint(introduced))
		}
		if introduced {
			upsert("<!-- tfpls:introduced -->", "ℹ️ This pull request adds the pipeline (`"+path+"`), so **every workspace is planned once**. `/tofu` comments are answered after it is merged into the default branch: GitHub runs comment workflows from there.")
		}
	case "no-workspaces":
		upsert("<!-- tfpls:no-workspaces -->", "ℹ️ No workspaces were found. The default layout is one folder per workspace under `workspaces/`. Describe yours in `"+orDefault(e.Getenv("CONFIG"), ".tfpls.yml")+"`, for example separate root modules `stacks: [{ name: dev, dir: envs/dev, workspaces: [default], watch: [modules] }, ...]`, or one root module with var files `stacks: [{ name: app, dir: ., workspaces: [dev, prod], var_file: \"vars/{workspace}.tfvars\", workspace_paths: [\"vars/{workspace}.tfvars\"] }]`. More: "+docs+"/docs/getting-started.md#another-layout. This comment disappears once workspaces are found.")
	case "clear":
		if pr == "" || repo == "" {
			return 0
		}
		cs, err := listComments(e.GH, repo, pr)
		if err != nil {
			return 0
		}
		for _, c := range cs {
			if c.isBot() && strings.HasPrefix(c.Body, "<!-- tfpls:no-workspaces -->") {
				_, _ = e.GH.Run("api", "-X", "DELETE", fmt.Sprintf("repos/%s/issues/comments/%d", repo, c.ID))
			}
		}
	default:
		fmt.Fprintln(e.Stderr, "usage: tfpls pr-notes introduced|no-workspaces|clear")
		return 2
	}
	return 0
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

const outdatedMarker = "is not affected by the latest commit"

// workspaceName returns NAME of a first line `<!-- tfpls:NAME -->` (NAME without a space), or "".
func workspaceName(body string) string {
	const pre, suf = "<!-- tfpls:", " -->"
	if !strings.HasPrefix(body, pre) {
		return ""
	}
	rest := body[len(pre):]
	i := strings.Index(rest, suf)
	if i <= 0 || strings.Contains(rest[:i], " ") {
		return ""
	}
	return rest[:i]
}

// Outdated implements `tfpls mark-outdated`: env PR, NAMES (JSON array), SHA, GITHUB_REPOSITORY.
func Outdated(e Env) int {
	repo, pr := e.Getenv("GITHUB_REPOSITORY"), e.Getenv("PR")
	if repo == "" || pr == "" {
		fmt.Fprintln(e.Stderr, "GITHUB_REPOSITORY and PR are required")
		return 1
	}
	var names []any
	raw := e.Getenv("NAMES")
	if raw == "" {
		raw = "[]"
	}
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		// The Bash read anything that was not an array as "no workspace selected" and marked every plan outdated. A
		// wrong banner on a current plan misleads, so an unreadable list marks nothing.
		fmt.Fprintln(e.Stdout, "::warning::names is not a JSON array: no comment was marked")
		return 0
	}
	selected := map[string]bool{}
	for _, n := range names {
		if s, ok := n.(string); ok {
			selected[s] = true
		}
	}
	short := []rune(e.Getenv("SHA"))
	if len(short) > 7 {
		short = short[:7]
	}
	banner := "> ⏭️ **Outdated:** this workspace is not affected by the latest commit (`" + string(short) + "`). The plan below is from an earlier commit and is kept for reference only."

	cs, err := listComments(e.GH, repo, pr)
	if err != nil {
		fmt.Fprintln(e.Stdout, "::warning::could not list comments")
		return 0
	}
	newest := map[string]comment{}
	var order []string
	for _, c := range cs {
		if !c.isBot() {
			continue
		}
		n := workspaceName(c.Body)
		if n == "" || n == "introduced" || n == "no-workspaces" { // notes of the pipeline, not a workspace plan
			continue
		}
		if old, ok := newest[n]; !ok {
			order = append(order, n)
			newest[n] = c
		} else if c.ID > old.ID {
			newest[n] = c
		}
	}
	for _, n := range order {
		c := newest[n]
		if selected[n] || strings.Contains(c.Body, outdatedMarker) {
			continue
		}
		lines := strings.Split(c.Body, "\n")
		body := lines[0] + "\n" + banner + "\n" + strings.Join(lines[1:], "\n")
		if err := patch(e.GH, repo, c.ID, body); err != nil {
			fmt.Fprintf(e.Stdout, "::warning::could not update comment %d\n", c.ID)
		} else {
			fmt.Fprintf(e.Stdout, "marked outdated: %s\n", n)
		}
	}
	return 0
}
