package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const isoLayout = "2006-01-02T15:04:05Z"

// ghFunc runs `gh <args>` and returns its standard output (the gh.Client.Run of the real binary; a fake in tests).
type ghFunc func(args ...string) (string, error)

// str: a JSON string value, or "" for anything else
func str(v any) string { s, _ := v.(string); return s }

// interp: a value as string interpolation "\(.x)" prints it (null -> "null")
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

// alt: `.x // def`: null and false give the default
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

func fetchIssue(gh ghFunc, repo, n string) (issue, error) {
	raw, err := gh("api", "repos/"+repo+"/issues/"+n)
	if err != nil {
		return issue{}, err
	}
	var m map[string]any
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&m); err != nil || m == nil || m["number"] == nil {
		return issue{}, errors.New("the answer is not an issue object")
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

// fetchComments: every page of the issue's comments. An error is an error (the caps count on them: no comments read must never
// mean no runs counted, the Bash gate failed open here).
func fetchComments(gh ghFunc, repo, n string) ([]comment, error) {
	raw, err := gh("api", "--paginate", "repos/"+repo+"/issues/"+n+"/comments?per_page=100")
	if err != nil {
		return nil, fmt.Errorf("could not read the comments of issue #%s: %w", n, err)
	}
	all := []comment{}
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	for {
		var page []comment
		if err := d.Decode(&page); err == io.EOF {
			return all, nil
		} else if err != nil {
			return nil, fmt.Errorf("the comments of issue #%s are not JSON: %w", n, err)
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

// since: 24 hours before now; an error when now is not an ISO time
func since(now string) (string, error) {
	t, err := time.Parse(isoLayout, now)
	if err != nil {
		return "", fmt.Errorf("TFPLS_NOW %q is not an ISO time (YYYY-MM-DDTHH:MM:SSZ)", now)
	}
	return t.Add(-24 * time.Hour).Format(isoLayout), nil
}

// runsToday: agent jobs of the workflow started since `from` (a skipped agent job costs nothing), over every page of runs (the
// Bash gate saw only the first 50). Any API or JSON error is an error: the daily cap never fails open.
func runsToday(gh ghFunc, repo, workflow, from string) (int, error) {
	raw, err := gh("api", "--paginate", "-X", "GET", "repos/"+repo+"/actions/workflows/"+workflow+"/runs", "-f", "created=>="+from, "-f", "per_page=100")
	if err != nil {
		return 0, fmt.Errorf("could not list the runs of %s: %w", workflow, err)
	}
	var ids []string
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	for {
		var page struct {
			WorkflowRuns []struct {
				ID json.Number `json:"id"`
			} `json:"workflow_runs"`
		}
		if err := d.Decode(&page); err == io.EOF {
			break
		} else if err != nil {
			return 0, fmt.Errorf("the runs of %s are not JSON: %w", workflow, err)
		}
		for _, r := range page.WorkflowRuns {
			ids = append(ids, r.ID.String())
		}
	}
	n := 0
	for _, id := range ids {
		raw, err := gh("api", "--paginate", "repos/"+repo+"/actions/runs/"+id+"/jobs?per_page=100")
		if err != nil {
			return 0, fmt.Errorf("could not list the jobs of run %s: %w", id, err)
		}
		d := json.NewDecoder(strings.NewReader(raw))
		for {
			var jobs struct {
				Jobs []struct {
					Name       any `json:"name"`
					Conclusion any `json:"conclusion"`
				} `json:"jobs"`
			}
			if err := d.Decode(&jobs); err == io.EOF {
				break
			} else if err != nil {
				return 0, fmt.Errorf("the jobs of run %s are not JSON: %w", id, err)
			}
			for _, j := range jobs.Jobs {
				if str(j.Name) == "agent" && str(j.Conclusion) != "skipped" {
					n++
				}
			}
		}
	}
	return n, nil
}
