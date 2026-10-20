// Package waitfor polls this workflow run's jobs until every named predecessor has completed, and fails when one of
// them did not succeed (replaces actions/wait-for/wait.sh).
package waitfor

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// GH is what the package needs from the gh CLI: the stdout of `gh <args>`.
type GH interface {
	Run(args ...string) (string, error)
}

// Env is everything Run reads from the outside, injected so that it can be tested.
type Env struct {
	Getenv func(string) string
	Stdout io.Writer
	GH     GH
	Now    func() time.Time
	Sleep  func(time.Duration)
}

type job struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	Conclusion *string `json:"conclusion"`
}

// Budget is the wait budget in minutes: never the whole job timeout (a job that waited its full budget would be
// killed mid-apply holding the lock). Junk or empty means the default of 50.
func Budget(timeout string) int {
	t := 50
	if n, err := strconv.Atoi(timeout); err == nil && n >= 0 && timeout != "" && isDigits(timeout) {
		t = n
	}
	switch {
	case t > 30:
		return t - 15
	case t >= 2:
		return t / 2
	}
	return t
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func parseJobs(out string) ([]job, error) {
	var jobs []job
	dec := json.NewDecoder(strings.NewReader(out))
	for dec.More() {
		var j job
		if err := dec.Decode(&j); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

// find matches a job by exact name, or by the tail "/ <label>" (a matrix job of a reusable workflow) or " (<label>)".
func find(jobs []job, label string) *job {
	for i := range jobs {
		n := jobs[i].Name
		if n == label || strings.HasSuffix(n, "/ "+label) || strings.HasSuffix(n, " ("+label+")") {
			return &jobs[i]
		}
	}
	return nil
}

// Run returns the exit code: 0 all predecessors succeeded, 1 one failed or the budget ran out.
func Run(e Env) int {
	budget := Budget(e.Getenv("TIMEOUT"))
	poll := 10 * time.Second
	if v := e.Getenv("TFPLS_POLL"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			poll = time.Duration(f * float64(time.Second))
		}
	}
	deadline := e.Now().Add(time.Duration(budget) * time.Minute)
	var want []string
	for _, w := range strings.Split(e.Getenv("WAIT_FOR"), ",") {
		if w = strings.ReplaceAll(w, " ", ""); w != "" {
			want = append(want, w)
		}
	}
	fmt.Fprintf(e.Stdout, "waiting for: %s\n", strings.Join(want, " "))
	url := fmt.Sprintf("repos/%s/actions/runs/%s/jobs?per_page=100", e.Getenv("REPO"), e.Getenv("RUN_ID"))
	for {
		out, err := e.GH.Run("api", url, "--paginate", "--jq", ".jobs[] | {name, status, conclusion}")
		var jobs []job
		if err == nil {
			jobs, err = parseJobs(out)
		}
		if err != nil {
			fmt.Fprintln(e.Stdout, "::warning::could not read the jobs of this run (does the workflow grant `actions: read`?), retrying")
		} else {
			var pending []string
			for _, w := range want {
				j := find(jobs, w)
				if j == nil {
					pending = append(pending, w+" (not started)")
					continue
				}
				if j.Status != "completed" {
					pending = append(pending, w+" ("+j.Status+")")
					continue
				}
				if j.Conclusion == nil || *j.Conclusion != "success" {
					c := ""
					if j.Conclusion != nil {
						c = *j.Conclusion
					}
					fmt.Fprintf(e.Stdout, "::error::not applied: `%s` has to succeed first and finished as %s\n", w, c)
					return 1
				}
			}
			if len(pending) == 0 {
				fmt.Fprintln(e.Stdout, "all predecessors succeeded")
				return 0
			}
			fmt.Fprintf(e.Stdout, "still waiting: %s \n", strings.Join(pending, " "))
		}
		if !e.Now().Before(deadline) {
			fmt.Fprintf(e.Stdout, "::error::gave up waiting for predecessors after %d minutes\n", budget)
			return 1
		}
		e.Sleep(poll)
	}
}
