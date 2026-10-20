package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"
)

const isoLayout = "2006-01-02T15:04:05Z"

// ghFunc runs `gh <args>` and returns its standard output; stderr receives gh's error output.
type ghFunc func(stderr io.Writer, args ...string) ([]byte, error)

func ghExec(stderr io.Writer, args ...string) ([]byte, error) {
	var out bytes.Buffer
	c := exec.Command("gh", args...)
	c.Stdout, c.Stderr = &out, stderr
	err := c.Run()
	return out.Bytes(), err
}

// str: a JSON string value, or "" for anything else
func str(v any) string { s, _ := v.(string); return s }

// interp: a value as jq's string interpolation "\(.x)" prints it (null -> "null")
func interp(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "null"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// alt: jq's `.x // def`: null and false give the default
func alt(v any, def string) string {
	if v == nil || v == false {
		return def
	}
	return interp(v)
}

type issue struct {
	number, title any
	isPR          bool
	labels        []string
	assoc, author string
	body, rawBody string
}

func fetchIssue(gh ghFunc, repo, n string, stderr io.Writer) (issue, error) {
	raw, err := gh(stderr, "api", "repos/"+repo+"/issues/"+n)
	if err != nil {
		return issue{}, err
	}
	var m map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&m); err != nil || m == nil {
		return issue{}, errors.New("not an issue object")
	}
	is := issue{number: m["number"], title: m["title"]}
	if pr, ok := m["pull_request"]; ok && pr != nil && pr != false {
		is.isPR = true
	}
	ls, _ := m["labels"].([]any)
	for _, l := range ls {
		lm, _ := l.(map[string]any)
		if name := lm["name"]; name != nil {
			is.labels = append(is.labels, interp(name))
		} else {
			is.labels = append(is.labels, "")
		}
	}
	is.assoc = alt(m["author_association"], "NONE")
	user, _ := m["user"].(map[string]any)
	is.author = alt(user["login"], "")
	// Bash keeps the body in a variable, and $(...) drops its trailing newlines
	is.rawBody = alt(m["body"], "")
	is.body = strings.TrimRight(alt(m["body"], ""), "\n")
	return is, nil
}

type comment struct {
	User struct {
		Login any `json:"login"`
		Type  any `json:"type"`
	} `json:"user"`
	Assoc     any `json:"author_association"`
	Body      any `json:"body"`
	CreatedAt any `json:"created_at"`
}

func (c comment) bot() bool { return str(c.User.Type) == "Bot" }

// fetchComments: every page of the issue's comments; any error gives no comments (as the Bash gate does)
func fetchComments(gh ghFunc, repo, n string) []comment {
	raw, err := gh(io.Discard, "api", "--paginate", "repos/"+repo+"/issues/"+n+"/comments?per_page=100")
	if err != nil {
		return nil
	}
	var all []comment
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	for {
		var page []comment
		if err := d.Decode(&page); err == io.EOF {
			return all
		} else if err != nil {
			return nil
		}
		all = append(all, page...)
	}
}

// saidSinceLastRun: our bots posted the status `key` after the agent last ran on this issue
func saidSinceLastRun(cs []comment, key string) bool {
	marker := "<!-- tfpls-agent-status: " + key + " -->"
	said := false
	for _, c := range cs {
		if !c.bot() {
			continue
		}
		b := alt(c.Body, "")
		if strings.Contains(b, "<!-- tfpls-agent-run:") {
			said = false
		}
		if strings.Contains(b, marker) {
			said = true
		}
	}
	return said
}

// countRuns: the agent's run reports for this phase (only our bots' comments count)
func countRuns(cs []comment, phase string) int {
	n := 0
	for _, c := range cs {
		if c.bot() && strings.Contains(alt(c.Body, ""), "tfpls-agent-run: "+phase) {
			n++
		}
	}
	return n
}

// since: 24 hours before now, or "" when now is not an ISO time (jq fails the same way)
func since(now string) string {
	t, err := time.Parse(isoLayout, now)
	if err != nil {
		return ""
	}
	return t.Add(-24 * time.Hour).Format(isoLayout)
}

// runsToday: agent jobs of the workflow started since `from` (a skipped agent job costs nothing); errors count as 0
func runsToday(gh ghFunc, repo, workflow, from string) int {
	raw, err := gh(io.Discard, "api", "-X", "GET", "repos/"+repo+"/actions/workflows/"+workflow+"/runs", "-f", "created=>="+from, "-f", "per_page=50")
	if err != nil {
		return 0
	}
	var runs struct {
		WorkflowRuns []struct {
			ID json.Number `json:"id"`
		} `json:"workflow_runs"`
	}
	if json.Unmarshal(raw, &runs) != nil {
		return 0
	}
	n := 0
	for _, r := range runs.WorkflowRuns {
		raw, err := gh(io.Discard, "api", "repos/"+repo+"/actions/runs/"+r.ID.String()+"/jobs")
		if err != nil {
			continue
		}
		var jobs struct {
			Jobs []struct {
				Name       any `json:"name"`
				Conclusion any `json:"conclusion"`
			} `json:"jobs"`
		}
		if json.Unmarshal(raw, &jobs) != nil {
			continue
		}
		for _, j := range jobs.Jobs {
			if str(j.Name) == "agent" && str(j.Conclusion) != "skipped" {
				n++
			}
		}
	}
	return n
}
