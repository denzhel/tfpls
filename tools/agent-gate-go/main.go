// Command agent-gate is a SPIKE port of tools/agent-gate.sh to Go (never shipped; the Bash gate is the production code).
// Same inputs (environment), outputs (GITHUB_OUTPUT lines), files, exit codes and message texts. It calls `gh` as a process.
//
//	agent-gate elaborate|implement
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// the status texts the gate posts (each ends with what happens next); identical to the Bash gate
const (
	sayOutside = "Thank you for the issue. A maintainer will read it.\n" +
		"The issue agent fills in issues of the owner and collaborators by itself. For an issue from outside the project it runs only when a maintainer asks for it, and then it only proposes, in a comment: your text stays as you wrote it.\n" +
		"**Next:** a maintainer replies here, or comments `/elaborate` to have the agent propose Outcome, Tasks and criteria."
	sayElaborated = "The issue agent did not run: this issue is already filled in (Outcome, Tasks, Acceptance criteria and Test criteria), so the gate labelled it `ready`.\n" +
		"**Next:** the owner comments `/implement` (optionally with instructions) to start the implementation; `/elaborate` has the agent review and complete it first."
	sayElaboratedOpen = "The issue agent did not run: this issue is already filled in, and it is labelled `needs-info` (open questions).\n" +
		"**Next:** answer the questions in a comment (any comment of the owner or a collaborator runs the agent again), or replace `needs-info` by `ready` when nothing is open."
	sayElaborateRefused = "`/elaborate` was ignored: only the owner and collaborators can start the issue agent.\n" +
		"**Next:** nothing to do for you; a maintainer decides how this issue moves on."
	sayNotOwner = "`/implement` was ignored: only the repository owner can start an implementation.\n" +
		"**Next:** the owner comments `/implement` when the issue should be built; collaborators can answer questions or comment `/elaborate`."
	sayImplOutside = "`/implement` was ignored: the agent implements only issues written by the owner or a collaborator (text from outside the project never reaches it).\n" +
		"**Next:** open a new issue with the agreed text (the agent fills it in), then comment `/implement` there."
	sayImplNeedsInfo = "`/implement` was ignored: the issue still has open questions (label `needs-info`).\n" +
		"**Next:** answer them in a comment; any comment of the owner or a collaborator runs the agent again, and it labels the issue `ready` when nothing is open."
	sayImplNotReady = "`/implement` was ignored: the issue is not labelled `ready` and is not filled in yet (Outcome, Tasks, Acceptance criteria or Test criteria are missing or only placeholders).\n" +
		"**Next:** comment `/elaborate` to have the agent fill it in (it labels the issue `ready` when nothing is open), then comment `/implement`."
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, ghExec, os.Stdout, os.Stderr))
}

// trusted: the associations whose words reach the agent as they are
func trusted(assoc string) bool {
	switch assoc {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	}
	return false
}

type gate struct {
	phase, repo, issue, out, work, now string
	env                                func(string) string
	gh                                 ghFunc
	stdout, stderr                     io.Writer
	comments                           []comment
	commentsLoaded                     bool
}

// envOr is Bash's ${VAR:-default}: unset and empty both give the default
func (g *gate) envOr(k, def string) string {
	if v := g.env(k); v != "" {
		return v
	}
	return def
}

func (g *gate) output(lines ...string) {
	f, err := os.OpenFile(g.out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(g.stderr, "agent-gate: %v\n", err)
		return
	}
	defer f.Close()
	for _, l := range lines {
		fmt.Fprintln(f, l)
	}
}

// skip: no run; when a person can act on it (key set), one status comment that says why and what happens next
func (g *gate) skip(reason, key, text string) int {
	fmt.Fprintf(g.stdout, "gate: no run (%s)\n", reason)
	g.output("run=false", "reason="+reason)
	if key == "" {
		return 0
	}
	if saidSinceLastRun(g.loadComments(), key) {
		fmt.Fprintf(g.stdout, "gate: already said on the issue (%s)\n", key)
		return 0
	}
	p := g.work + "/gate-comment.md"
	if err := os.WriteFile(p, []byte(fmt.Sprintf("%s\n\n<!-- tfpls-agent-status: %s -->\n", text, key)), 0o644); err != nil {
		fmt.Fprintf(g.stderr, "agent-gate: %v\n", err)
	}
	g.output("comment_file=" + p)
	return 0
}

func (g *gate) loadComments() []comment {
	if !g.commentsLoaded {
		g.comments = fetchComments(g.gh, g.repo, g.issue)
		g.commentsLoaded = true
	}
	return g.comments
}

// firstLine: the first line of a comment without CRs and leading blanks; command: its first word, lower case
func firstLine(body string) string {
	l, _, _ := strings.Cut(body, "\n")
	return strings.TrimLeft(strings.ReplaceAll(l, "\r", ""), " \t\n\v\f\r")
}

func command(first string) string {
	f := strings.FieldsFunc(first, func(r rune) bool { return r == ' ' || r == '\t' })
	if len(f) == 0 {
		return ""
	}
	return strings.ToLower(f[0])
}

// wordsAfter: the comment without CRs and without the leading /command on its first line, trailing newlines dropped
func wordsAfter(body string) string {
	body = strings.ReplaceAll(body, "\r", "")
	l, rest, more := strings.Cut(body, "\n")
	l = reCommand.ReplaceAllString(l, "")
	if more {
		l += "\n" + rest
	}
	return strings.TrimRight(l, "\n")
}

// blank: nothing but ASCII white space (Bash: tr -d '[:space:]')
func blank(s string) bool { return strings.Trim(s, " \t\n\v\f\r") == "" }

func run(args []string, getenv func(string) string, gh ghFunc, stdout, stderr io.Writer) int {
	phase := ""
	if len(args) > 0 {
		phase = args[0]
	}
	if phase != "elaborate" && phase != "implement" {
		fmt.Fprintln(stderr, "usage: tools/agent-gate.sh elaborate|implement")
		return 2
	}
	g := &gate{phase: phase, env: getenv, gh: gh, stdout: stdout, stderr: stderr}
	for _, k := range []string{"REPO", "ISSUE"} {
		if getenv(k) == "" {
			fmt.Fprintf(stderr, "agent-gate: %s: parameter null or not set\n", k)
			return 1
		}
	}
	g.repo, g.issue = getenv("REPO"), getenv("ISSUE")
	g.out = g.envOr("GITHUB_OUTPUT", os.DevNull)
	g.work = g.envOr("AGENT_WORK", g.envOr("RUNNER_TEMP", "/tmp"))
	g.now = g.envOr("TFPLS_NOW", time.Now().UTC().Format(isoLayout))
	_ = os.MkdirAll(g.work, 0o755)

	// ---- cheap checks first: nothing below costs a model run
	if g.envOr("ENABLED", "true") == "false" {
		return g.skip("the agent is switched off (repository variable TFPLS_AGENT_ENABLED=false)", "", "")
	}
	if g.envOr("SENDER_TYPE", "User") == "Bot" {
		return g.skip("the event was caused by a bot", "", "")
	}
	is, err := fetchIssue(gh, g.repo, g.issue, stderr)
	if err != nil {
		fmt.Fprintf(stdout, "::error::the gate could not read issue #%s\n", g.issue)
		return 1
	}
	if is.isPR {
		return g.skip("it is a pull request", "", "")
	}
	labels := is.labels
	hasLabel := func(l string) bool {
		for _, x := range labels {
			if x == l {
				return true
			}
		}
		return false
	}

	commentBody := getenv("COMMENT_BODY")
	first := firstLine(commentBody)
	cmd := command(first)
	actor := g.envOr("ACTOR_ASSOC", "NONE")
	event := getenv("EVENT_NAME")

	// ---- who and what may start a run
	mode := phase
	if phase == "elaborate" && !trusted(is.assoc) {
		mode = "proposal"
	}
	started, instructions := "", ""
	switch phase {
	case "elaborate":
		switch event {
		case "issues":
			if !trusted(is.assoc) {
				return g.skip("the issue was not opened by the owner or a collaborator", "outside", sayOutside)
			}
			if elaborated(is.body) {
				if hasLabel("needs-info") {
					return g.skip("the issue is already elaborated", "elaborated", sayElaboratedOpen)
				}
				if !hasLabel("ready") {
					g.output("label=ready")
				}
				return g.skip("the issue is already elaborated", "elaborated", sayElaborated)
			}
			started = "the issue was opened"
		case "workflow_dispatch":
			// an explicit request of the owner (the workflow filter allows only the owner): it runs even on a filled-in issue
			if actor != "OWNER" {
				return g.skip("a manual run is for the owner", "", "")
			}
			started = "a manual run of the owner"
		case "issue_comment":
			if cmd == "/elaborate" {
				if !trusted(actor) {
					return g.skip("/elaborate is for the owner and collaborators", "elaborate-refused", sayElaborateRefused)
				}
				started = fmt.Sprintf("`/elaborate` of a maintainer (%s)", getenv("ACTOR_ASSOC"))
				instructions = wordsAfter(commentBody)
			} else {
				switch {
				case strings.HasPrefix(first, "/"):
					return g.skip("the comment is a command, not an answer", "", "")
				case !hasLabel("needs-info"):
					return g.skip("the issue is not waiting for answers", "", "")
				case !trusted(actor):
					return g.skip("the comment is not from the owner or a collaborator", "", "")
				}
				started = fmt.Sprintf("an answer of a maintainer (%s) on an issue labelled `needs-info`", getenv("ACTOR_ASSOC"))
			}
		default:
			return g.skip("not an issue, a comment or a manual run", "", "")
		}
	case "implement":
		if event != "issue_comment" && event != "workflow_dispatch" {
			return g.skip("not a comment or a manual run", "", "")
		}
		if cmd != "/implement" {
			return g.skip("the comment is not /implement", "", "")
		}
		// owner only, by the owner's decision; the workflow repeats this check before the agent job
		if actor != "OWNER" {
			return g.skip("/implement is for the owner", "implement-not-owner", sayNotOwner)
		}
		if !trusted(is.assoc) {
			return g.skip("the issue was written by someone who is not the owner or a collaborator", "implement-outside", sayImplOutside)
		}
		if hasLabel("needs-info") {
			return g.skip("the issue still has open questions", "implement-needs-info", sayImplNeedsInfo)
		}
		if !hasLabel("ready") {
			// filled in and nothing open: the gate labels it ready itself instead of refusing
			if !elaborated(is.body) {
				return g.skip("the issue is not ready", "implement-not-ready", sayImplNotReady)
			}
			g.output("label=ready")
			labels = append(labels, "ready")
		}
		started = "`/implement` of the owner"
		if event == "workflow_dispatch" {
			started = "a manual run of the owner"
		}
	}

	// ---- run caps (per issue and per 24 hours): an agent run must never be unbounded
	cp := capsFor(phase, g.issue, g.env)
	perIssue := countRuns(g.loadComments(), phase)
	if over(perIssue, cp.maxIssue, stderr) {
		return g.skip(fmt.Sprintf("the per-issue cap is reached (%d of %s %s runs)", perIssue, cp.maxIssue, phase), "cap-issue-"+phase,
			fmt.Sprintf("The agent did not run: it already ran %d times (%s) on this issue; the limit is %s.\n**Next:** %s, or raise the repository variable `%s` and comment `/%s` again.",
				perIssue, phase, cp.maxIssue, cp.byHand, cp.varIssue, phase))
	}
	today := runsToday(gh, g.repo, cp.workflow, since(g.now))
	if over(today, cp.maxDay, stderr) {
		day, _, _ := strings.Cut(g.now, "T")
		return g.skip(fmt.Sprintf("the daily cap is reached (%d of %s %s runs in 24 hours)", today, cp.maxDay, phase), "cap-day-"+phase+"-"+day,
			fmt.Sprintf("The agent did not run: the daily limit of agent runs (%s %s runs in 24 hours) is reached.\n**Next:** comment `/%s` again later (the limit counts the last 24 hours), or raise the repository variable `%s`.",
				cp.maxDay, phase, phase, cp.varDay))
	}

	// ---- the trusted copy of the issue for the agent (it never calls `gh issue view` itself)
	var b bytes.Buffer
	writeCopy(&b, is, labels, g.loadComments(), mode, started, instructions)
	f := g.work + "/issue.md"
	if err := os.WriteFile(f, b.Bytes(), 0o644); err != nil {
		fmt.Fprintf(stderr, "agent-gate: %v\n", err)
	}
	g.output("run=true", "reason=ok", "mode="+mode, "issue_file="+f)

	// the owner's own words after /implement are instructions for this run
	if phase == "implement" {
		if note := wordsAfter(commentBody); !blank(note) {
			p := g.work + "/implement-note.md"
			_ = os.WriteFile(p, []byte(note+"\n"), 0o644)
			g.output("note_file=" + p)
		}
	}
	fmt.Fprintf(stdout, "gate: run (%s #%s, mode %s; %d of %s on this issue, %d of %s in 24 hours)\n",
		phase, g.issue, mode, perIssue, cp.maxIssue, today, cp.maxDay)
	return 0
}

type caps struct{ maxIssue, maxDay, workflow, varIssue, varDay, byHand string }

func capsFor(phase, issue string, env func(string) string) caps {
	or := func(k, d string) string {
		if v := env(k); v != "" {
			return v
		}
		return d
	}
	if phase == "elaborate" {
		return caps{or("MAX_ELAB_ISSUE", "3"), or("MAX_ELAB_DAY", "20"), "claude-elaborate.yml",
			"TFPLS_AGENT_MAX_ELAB_PER_ISSUE", "TFPLS_AGENT_MAX_ELAB_PER_DAY", "finish the issue by hand (label it `ready` when nothing is open)"}
	}
	return caps{or("MAX_IMPL_ISSUE", "2"), or("MAX_IMPL_DAY", "5"), "claude-implement.yml",
		"TFPLS_AGENT_MAX_IMPL_PER_ISSUE", "TFPLS_AGENT_MAX_IMPL_PER_DAY", "continue by hand on the branch `agent/issue-" + issue + "`"}
}

// over: count >= limit. Parity with Bash `[ "$n" -ge "$max" ]`: a limit that is not an integer prints an error and never stops
// the run (the cap fails open; a bug of the Bash gate that the port keeps on purpose, see the spike report).
func over(n int, limit string, stderr io.Writer) bool {
	m, err := strconv.Atoi(strings.TrimSpace(limit))
	if err != nil {
		fmt.Fprintf(stderr, "agent-gate: %s: integer expression expected\n", limit)
		return false
	}
	return n >= m
}
