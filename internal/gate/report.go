package gate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Report is the entry of `tfpls agent-report`: what an issue-agent run used, as one line in the run summary and one comment on the
// issue. The comment carries the marker that the gate counts for the per-issue cap, and it is posted for a failed run too (it cost
// something). Its last line says what happens next, from the outcome and the labels the agent left (every agent comment ends
// with one).
//
// Inputs (environment): PHASE (elaborate | implement), MODE (elaborate | proposal, from the gate; optional), ISSUE, REPO, RUN_URL,
// OUTCOME (the agent step's outcome), EXEC (the execution file of anthropics/claude-code-action: JSON or JSON lines, with a final
// "result" message), GH_TOKEN, GITHUB_STEP_SUMMARY. Best effort: it never fails the job (except when PHASE, ISSUE or REPO is missing).
func Report(e Env) int {
	for _, k := range []string{"PHASE", "ISSUE", "REPO"} {
		if e.Getenv(k) == "" {
			fmt.Fprintf(e.Stderr, "agent-report: %s: parameter null or not set\n", k)
			return 1
		}
	}
	phase, issue, repo := e.Getenv("PHASE"), e.Getenv("ISSUE"), e.Getenv("REPO")
	outcome := e.Getenv("OUTCOME")
	if outcome == "" {
		outcome = "unknown"
	}

	res := readResult(e.Getenv("EXEC"))
	turns, secs, cost := "?", "?", "not reported"
	if res != nil {
		turns = alt(res["num_turns"], "?")
		if ms, ok := res["duration_ms"].(json.Number); ok {
			if f, err := ms.Float64(); err == nil {
				secs = strconv.FormatFloat(math.Round(f/1000), 'f', 0, 64)
			}
		}
		if c, ok := res["total_cost_usd"].(json.Number); ok {
			if f, err := c.Float64(); err == nil {
				cost = strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64)
			}
		}
	}
	unit := ""
	if cost != "not reported" {
		unit = " USD"
	}
	line := fmt.Sprintf("Agent run (%s): %s, %s turns, %s s, cost %s%s", phase, outcome, turns, secs, cost, unit)
	if res == nil {
		// no result record at all: the agent did not run (a green job can hide that). The Claude action skips itself, with a warning in
		// its log, when the workflow file of the run differs from the one on the default branch (a security check that also hits a
		// manual run from a branch that changed the workflow file) or when it cannot start.
		line = fmt.Sprintf("Agent run (%s): %s, **the agent produced no result** (no execution record; the Claude action may have skipped itself, see the run log)", phase, outcome)
	}
	if u := e.Getenv("RUN_URL"); u != "" {
		line += " ([run](" + u + "))"
	}

	// what went wrong or was left undone, so that a "success" without a result is never silent: the tool calls that were denied (a
	// tool list that is too narrow), the agent's last words, and for an implementation whether a pull request exists
	extra := ""
	if denials, _ := res["permission_denials"].([]any); len(denials) > 0 {
		var lines []string
		for _, d := range denials[:min(len(denials), 8)] {
			if l, ok := denialLine(d); ok {
				lines = append(lines, l)
			}
		}
		extra += fmt.Sprintf("\n**%d tool call(s) were denied** (not in the tool list):\n%s", len(denials), strings.Join(lines, "\n"))
	}
	pr := ""
	if phase == "implement" {
		pr = pullRequest(e, repo, issue)
		if pr != "" {
			extra += "\nResult: " + pr
		} else {
			extra += "\n**No pull request was opened for this run.**"
		}
	}
	if last := lastWords(res); last != "" {
		extra += "\n<details><summary>The agent's last words</summary>\n\n" + last + "\n\n</details>"
	}

	// what happens next: from the outcome and the labels after the run
	labels := issueLabels(e, repo, issue)
	hasLabel := func(l string) bool {
		for _, x := range labels {
			if x == l {
				return true
			}
		}
		return false
	}
	var next string
	switch {
	case phase == "implement" && pr != "":
		next = "**Next:** the owner reviews the pull request; the issue is closed when its evidence is in."
	case phase == "implement":
		next = "**Next:** read the agent's last words and the run log; the owner can comment `/implement` with instructions to try again."
	case res == nil || outcome != "success":
		next = "**Next:** the run did not finish (see the run log): comment `/elaborate` to try again, or fill in the issue by hand."
	case e.Getenv("MODE") == "proposal":
		next = "**Next:** a maintainer takes what fits from the proposal above (the issue body is unchanged); `/elaborate` with corrections updates the proposal."
	case hasLabel("needs-info"):
		next = "**Next:** answer the questions above in a comment (a plain \"yes\" accepts the defaults); any comment of the owner or a collaborator runs the agent again."
	case hasLabel("ready"):
		next = "**Next:** the owner comments `/implement` (optionally with instructions) to start the implementation; to change an assumption first, comment `/elaborate` with the change."
	default:
		next = "**Next:** the agent left no label: comment `/elaborate` to run it again, or label the issue `ready` by hand when the body is complete."
	}

	body := line
	if extra != "" {
		body += "\n" + extra
	}
	body += "\n\n" + next
	fmt.Fprintln(e.Stdout, body)
	if f := e.Getenv("GITHUB_STEP_SUMMARY"); f != "" {
		if w, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(w, body)
			w.Close()
		}
	}
	if _, err := e.GH("issue", "comment", issue, "--repo", repo, "--body", body+"\n<!-- tfpls-agent-run: "+phase+" -->"); err != nil {
		fmt.Fprintln(e.Stdout, "::warning::could not post the agent run report")
	}
	return 0
}

// readResult: the last object of type "result" in the execution file (JSON or JSON lines; arrays are flattened), or nil when the
// file is missing or unreadable, is not JSON, or has no result (jq -s failed or gave null the same way).
func readResult(path string) map[string]any {
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var last map[string]any
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, i := range x {
				walk(i)
			}
		case map[string]any:
			if x["type"] == "result" {
				last = x
			}
		}
	}
	for {
		var v any
		if err := d.Decode(&v); err == io.EOF {
			return last
		} else if err != nil {
			return nil
		}
		walk(v)
	}
}

// denialLine: "- `Tool`: what it tried" (one line, at most 110 characters of it)
func denialLine(d any) (string, bool) {
	m, ok := d.(map[string]any)
	if !ok {
		return "", false
	}
	what := ""
	switch in := m["tool_input"].(type) {
	case map[string]any:
		for _, k := range []string{"command", "file_path"} {
			if v := in[k]; v != nil && v != false {
				what = interp(v)
				break
			}
		}
		if what == "" {
			b, _ := json.Marshal(in)
			what = string(b)
		}
	default:
		what = interp(in)
	}
	what = strings.Join(strings.FieldsFunc(what, func(r rune) bool { return r == '\r' || r == '\n' }), " ")
	if r := []rune(what); len(r) > 110 {
		what = string(r[:110])
	}
	return fmt.Sprintf("- `%s`: %s", interp(m["tool_name"]), what), true
}

// lastWords: the end of the agent's final message, 900 characters at most (the Bash `tail -c` cut bytes, in the middle of a character)
func lastWords(res map[string]any) string {
	v := res["result"]
	if v == nil {
		return ""
	}
	s := strings.TrimRight(interp(v), "\n")
	if utf8.RuneCountInString(s) > 900 {
		r := []rune(s)
		s = strings.TrimRight(string(r[len(r)-900:]), "\n")
	}
	return s
}

func pullRequest(e Env, repo, issue string) string {
	out, err := e.GH("pr", "list", "--repo", repo, "--head", "agent/issue-"+issue, "--state", "all", "--json", "number,url")
	if err != nil {
		return ""
	}
	var prs []struct {
		Number json.Number `json:"number"`
		URL    string      `json:"url"`
	}
	if json.Unmarshal([]byte(out), &prs) != nil || len(prs) == 0 {
		return ""
	}
	return fmt.Sprintf("pull request #%s: %s", prs[0].Number, prs[0].URL)
}

func issueLabels(e Env, repo, issue string) []string {
	out, err := e.GH("issue", "view", issue, "--repo", repo, "--json", "labels")
	if err != nil {
		return nil
	}
	var v struct {
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if json.Unmarshal([]byte(out), &v) != nil {
		return nil
	}
	var names []string
	for _, l := range v.Labels {
		names = append(names, l.Name)
	}
	return names
}
