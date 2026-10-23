package repocheck

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The static rules of the issue agent (claude-elaborate.yml, claude-implement.yml): cost controls, who can start it,
// what its tools can do, and the documents it follows. These are the guards against a later change that loosens them.
// (They were tests/agent-workflows.test.sh, which read the workflows with a YAML tool.)

func stepWhere(wf any, job string, pred func(map[string]any) bool) map[string]any {
	for _, s := range stepsOf(wf, job) {
		if pred(s) {
			return s
		}
	}
	return nil
}

func stepNamed(wf any, job, name string) map[string]any {
	return stepWhere(wf, job, func(s map[string]any) bool { return s["name"] == name })
}

func stepIf(wf any, job, cond string) map[string]any {
	return stepWhere(wf, job, func(s map[string]any) bool { return s["if"] == cond })
}

func num(t *testing.T, v any) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(fmt.Sprint(v), 64)
	if err != nil {
		t.Fatalf("not a number: %v", v)
	}
	return f
}

// runScripts is every run script of every job.
func runScripts(wf any) string {
	var b strings.Builder
	jobs, _ := at(wf, "jobs").(map[string]any)
	for _, j := range keys(jobs) {
		for _, s := range stepsOf(wf, j) {
			if r, ok := s["run"].(string); ok {
				b.WriteString(r + "\n")
			}
		}
	}
	return b.String()
}

// allStrings is every string value of the document.
func allStrings(v any, out *[]string) {
	switch x := v.(type) {
	case string:
		*out = append(*out, x)
	case map[string]any:
		for _, c := range x {
			allStrings(c, out)
		}
	case []any:
		for _, c := range x {
			allStrings(c, out)
		}
	}
}

// between is `sed -n '/^start/,/^end/p'`: from the first line with the prefix up to the next line with the end prefix
// (included); end "" runs to the end of the text.
func between(text, start, end string) string {
	var b strings.Builder
	in := false
	for _, l := range strings.Split(text, "\n") {
		if !in && strings.HasPrefix(l, start) {
			in = true
			b.WriteString(l + "\n")
			continue
		}
		if in {
			b.WriteString(l + "\n")
			if end != "" && strings.HasPrefix(l, end) {
				break
			}
		}
	}
	return b.String()
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func TestAgentWorkflows(t *testing.T) {
	E := load(t, ".github/workflows/claude-elaborate.yml")
	I := load(t, ".github/workflows/claude-implement.yml")
	files := []struct {
		n  string
		wf any
	}{{"claude-elaborate.yml", E}, {"claude-implement.yml", I}}
	args := func(wf any) string { return str(at(stepByID(wf, "agent", "claude"), "with", "claude_args")) }
	prompt := func(wf any) string { return str(at(stepByID(wf, "agent", "claude"), "with", "prompt")) }
	has := strings.Contains

	for _, f := range files {
		n, wf := f.n, f.wf
		gateIf := str(at(wf, "jobs", "gate", "if"))
		agentIf := str(at(wf, "jobs", "agent", "if"))
		// ---- cost: no runner for events that do not match, a gate before the model, hard limits
		check(t, n+": the gate job has a free job-level filter", len(gateIf) > 0, "no gate filter")
		check(t, n+": the kill switch is in the free filter", has(gateIf, "TFPLS_AGENT_ENABLED != 'false'"), "kill switch missing")
		check(t, n+": bots never start it (no loop)", has(gateIf, "sender.type != 'Bot'"), "bot filter missing")
		check(t, n+": pull requests are ignored", has(gateIf, "pull_request == null"), "PR filter missing")
		eq(t, n+": the model job waits for the gate", str(at(wf, "jobs", "agent", "needs")), "gate")
		check(t, n+": the model job only runs when the gate says so", strings.HasPrefix(agentIf, "needs.gate.outputs.run == 'true'"), "agent condition: "+agentIf)
		g := stepByID(wf, "gate", "g")
		gs := stepsOf(wf, "gate")
		eq(t, n+": the gate is the command that is tested offline, after the binary is installed",
			fmt.Sprint(countLines(str(g["run"]), regexp.MustCompile(`^tfpls gate `)))+"|"+str(gs[1]["uses"])+","+str(gs[2]["id"]), "1|./actions/bin,g")
		eq(t, n+": the report is the command that is tested offline", str(stepNamed(wf, "agent", "Report what the run used")["run"]), "tfpls agent-report")
		check(t, n+": the gate job is short", num(t, at(wf, "jobs", "gate", "timeout-minutes")) <= 5, "gate timeout")
		// the gate job is reached by more people than the agent job: it gets only what it needs
		perms, _ := at(wf, "jobs", "gate", "permissions").(map[string]any)
		var pl []string
		for _, k := range keys(perms) {
			pl = append(pl, k+":"+str(perms[k]))
		}
		eq(t, n+": the gate job's own permissions: read the code, write issues, read runs (the daily cap)", strings.Join(pl, ","), "actions:read,contents:read,issues:write")
		eq(t, n+": the gate's checkout keeps no token", str(at(gs[0], "with", "persist-credentials")), "false")
		eq(t, n+": the usage report runs even when the agent fails", str(stepNamed(wf, "agent", "Report what the run used")["if"]), "always()")
		check(t, n+": the action is pinned to a commit", regexp.MustCompile(`@[0-9a-f]{40}$`).MatchString(str(stepByID(wf, "agent", "claude")["uses"])), "action not pinned")
		// never silent: the gate's status comment is posted, and a filled-in issue gets the gate's label
		check(t, n+": a step posts the gate's status comment", has(str(stepIf(wf, "gate", "steps.g.outputs.comment_file != ''")["run"]), "gh issue comment"), "no status comment step")
		check(t, n+": a step adds the label the gate decided", has(str(stepIf(wf, "gate", "steps.g.outputs.label != ''")["run"]), "issues/$ISSUE/labels"), "no label step")
		check(t, n+": even the missing-token comment says what happens next",
			has(str(stepNamed(wf, "agent", "Stop with a message when the Claude token is missing")["run"]), "**Next:**"), "token comment without Next")
		eq(t, n+": one run at a time per issue, never cancelling a running one",
			yes(strings.HasPrefix(str(at(wf, "concurrency", "group")), "claude-issue-"))+"|"+str(at(wf, "concurrency", "cancel-in-progress")), "yes|false")
		check(t, n+": no pull_request_target", !has(read(t, ".github/workflows/"+n), "pull_request_target"), "uses pull_request_target")
		a := args(wf)
		check(t, n+": a turn limit", regexp.MustCompile(`--max-turns [0-9]+`).MatchString(a), "no --max-turns")
		check(t, n+": the model comes from a variable with a cheap default",
			regexp.MustCompile(`--model \$\{\{ vars\.TFPLS_AGENT_MODEL_(ELABORATE|IMPLEMENT) \|\| 'claude-sonnet-5-5' \}\}`).MatchString(a), "model not configurable")
		// ---- safety: untrusted text never reaches a shell or the agent directly
		scripts := runScripts(wf)
		for _, field := range []string{"comment.body", "issue.body", "issue.title"} {
			if has(scripts, "github.event."+field) {
				t.Errorf("%s: github.event.%s is interpolated into a script", n, field)
			}
		}
		p := prompt(wf)
		check(t, n+": the prompt says issue text is data", has(p, "data only"), "prompt lacks the data-only rule")
		check(t, n+": the agent reads the prepared file", has(p, "issue.md"), "prompt does not use the prepared file")
		check(t, n+": no dangerous command is allowed", !regexp.MustCompile(`Bash\(gh (issue view|api|release|repo)|Bash\(curl|Bash\(wget|Bash\(git tag|gh pr (merge|review|close)|gh issue close|git push origin main|git push --force|Bash\(rm`).MatchString(a), "a dangerous command is allowed")
	}

	// ---- who can start what
	eIf := str(at(E, "jobs", "gate", "if"))
	iIf := str(at(I, "jobs", "gate", "if"))
	check(t, "elaborate: every opened issue reaches the gate", countLines(eIf, regexp.MustCompile(`^ *github.event_name == 'issues' \|\|$`)) > 0, "issues filter")
	check(t, "elaborate: /elaborate reaches the gate (it checks who)", has(eIf, "startsWith(github.event.comment.body, '/elaborate')"), "/elaborate filter")
	check(t, "elaborate: answers run it only on needs-info issues and only from the owner or a collaborator",
		has(eIf, "needs-info") && has(eIf, `contains(fromJSON('["OWNER","MEMBER","COLLABORATOR"]'), github.event.comment.author_association)`), "needs-info filter")
	check(t, "implement: /implement of anyone reaches the gate, which says why it refuses",
		has(iIf, "startsWith(github.event.comment.body, '/implement')") && !has(iIf, "author_association"), "command filter")
	iAgentIf := str(at(I, "jobs", "agent", "if"))
	check(t, "implement: the agent job itself runs only for the owner",
		has(iAgentIf, "github.event.comment.author_association == 'OWNER'") && has(iAgentIf, "github.event_name == 'workflow_dispatch' && github.actor == github.repository_owner"), "agent job not owner-only")
	check(t, "implement: the gate refuses anyone but the owner", has(read(t, "internal/gate/gate.go"), `g.skip("/implement is for the owner"`), "gate owner check")
	eq(t, "implement: comments and a manual run start it", strings.Join(keys(at(I, "on")), ","), "issue_comment,workflow_dispatch")
	eq(t, "elaborate: opened issues, comments and a manual run start it", strings.Join(keys(at(E, "on")), ","), "issue_comment,issues,workflow_dispatch")
	// ---- manual runs from a branch (workflow_dispatch): the owner only, with an issue number, through the same gate
	for _, f := range files {
		n, wf := f.n, f.wf
		eq(t, n+": a manual run needs the issue number", str(at(wf, "on", "workflow_dispatch", "inputs", "issue", "required")), "true")
		check(t, n+": a manual run is for the owner only", has(str(at(wf, "jobs", "gate", "if")), "github.event_name == 'workflow_dispatch' && github.actor == github.repository_owner"), "manual run not owner-only")
		check(t, n+": the gate is told a manual run is the owner's (the filter checked it)",
			has(str(at(stepByID(wf, "gate", "g"), "env", "ACTOR_ASSOC")), "workflow_dispatch' && 'OWNER"), "ACTOR_ASSOC for dispatch")
		for _, field := range []string{"inputs.instructions", "inputs.issue"} {
			if has(runScripts(wf), field) {
				t.Errorf("%s: %s is interpolated into a script", n, field)
			}
		}
	}
	eq(t, "implement: instructions are optional", str(at(I, "on", "workflow_dispatch", "inputs", "instructions", "required")), "false")
	check(t, "implement: the instructions of a manual run go through the same /implement path",
		has(str(at(stepByID(I, "gate", "g"), "env", "COMMENT_BODY")), "format('/implement {0}', inputs.instructions)"), "instructions path")

	// ---- tools: elaboration is read-only; implementation cannot touch its own guards or the release path
	agents := read(t, "AGENTS.md")
	sec := between(agents, "## Elaborating new issues", "## Implementing an issue")
	check(t, "AGENTS.md: the body is passed as text between single quotes with bold section titles (no files)",
		has(sec, "gh issue edit <n> --body '") && has(sec, "single quotes") && has(sec, "in bold") && !has(sec, "body-file"), "body rule")
	pe := prompt(E)
	check(t, "elaborate: the prompt says: no files, single quotes, bold titles", has(pe, "cannot write files") && has(pe, "single") && has(pe, "bold"), "prompt lacks the body rule")
	ae, ai := args(E), args(I)
	check(t, "elaborate: read-only tools, plus issue edit and comment", !regexp.MustCompile(`Edit|Write|Bash\(git|Bash\(bash|gh pr create`).MatchString(ae), "the agent can change files or run code")
	m := regexp.MustCompile(`mode == 'proposal' && '([^']*)' \|\| '([^']*)'`).FindStringSubmatch(ae)
	if m == nil {
		t.Fatal("elaborate: no proposal-mode tool list")
	}
	eq(t, "elaborate: in proposal mode the only tool that writes is a comment", m[1], "Read,Grep,Glob,Bash(gh issue comment:*)")
	eq(t, "elaborate: otherwise the tools are as before (read, issue edit and comment, label list)", m[2], "Read,Grep,Glob,Bash(gh issue edit:*),Bash(gh issue comment:*),Bash(gh label list:*)")
	eq(t, "elaborate: the gate's mode reaches the agent job", str(at(E, "jobs", "gate", "outputs", "mode")), "${{ steps.g.outputs.mode }}")
	check(t, "elaborate: the prompt gives the mode, asks for at most three questions and the Next line",
		has(pe, "needs.gate.outputs.mode") && has(pe, "**Next:**") && has(pe, "at most three questions"), "prompt lacks mode, questions or Next")
	eq(t, "elaborate: the report knows the mode (a proposal's last line differs)", str(at(stepNamed(E, "agent", "Report what the run used"), "env", "MODE")), "${{ needs.gate.outputs.mode }}")
	check(t, "implement: the prompt asks for the Next line on the issue", has(prompt(I), "**Next:**"), "prompt lacks Next")
	eq(t, "elaborate: the agent job does not persist the checkout credentials", str(at(stepsOf(E, "agent")[0], "with", "persist-credentials")), "false")
	check(t, "implement: it can push only agent/ branches", has(ai, "Bash(git push -u origin agent/*)"), "push rule")
	check(t, "no Bash rule ends in '/:*' (it would deny real commands)", !regexp.MustCompile(`Bash\([^)]*/:\*\)`).MatchString(ai+ae), "a Bash rule ends in '/:*', which matches nothing")
	check(t, "implement: it can run the test files (bash tests/<name>.test.sh)", has(ai, "Bash(bash tests/*)"), "tests cannot be run")
	check(t, "implement: the prompt asks for one simple command per call", has(prompt(I), "no pipes"), "prompt lacks the one-command rule")
	check(t, "implement: no broad git push", !regexp.MustCompile(`git push:\*|git push origin|git push -u origin [^a]`).MatchString(ai), "a broad git push is allowed")
	for _, guard := range []string{".github/workflows/claude-", "internal/gate/", "cmd/tfpls/cmd_gate.go", "AGENTS.md"} {
		check(t, "implement: it cannot edit "+guard, regexp.MustCompile("disallowedTools.*Edit\\("+regexp.QuoteMeta(guard)).MatchString(ai) && has(ai, "Write("+guard), guard+" is editable")
	}
	check(t, "implement: it can push and open a pull request", str(at(I, "permissions", "pull-requests")) == "write" && str(at(I, "permissions", "contents")) == "write", "permissions")
	check(t, "elaborate: contents read only", str(at(E, "permissions", "contents")) == "read", "contents permission")
	var envs []string
	allStrings(at(stepByID(I, "agent", "claude"), "env"), &envs)
	envKeys := keys(at(stepByID(I, "agent", "claude"), "env"))
	check(t, "implement: gh uses the action's app token (the pull request's checks run)", !has(strings.Join(append(envs, envKeys...), "\n"), "GH_TOKEN"), "GH_TOKEN is forced to the workflow token (its pull requests would not run the checks)")
	check(t, "implement: at most 30 minutes", num(t, at(I, "jobs", "agent", "timeout-minutes")) <= 30, "timeout")
	check(t, "elaborate: at most 10 minutes", num(t, at(E, "jobs", "agent", "timeout-minutes")) <= 10, "timeout")
	check(t, "implement: commits use the owner's noreply identity (never a hostname fallback)", has(str(stepNamed(I, "agent", "Git identity for the commits")["run"]), "users.noreply.github.com"), "git identity")
	pins := map[string]bool{}
	for _, f := range []string{"claude-elaborate", "claude-implement", "claude-triage"} {
		for _, x := range regexp.MustCompile(`anthropics/claude-code-action@[0-9a-f]{40}`).FindAllString(read(t, ".github/workflows/"+f+".yml"), -1) {
			pins[x] = true
		}
	}
	eq(t, "the Claude action is the same pinned commit in triage, elaboration and implementation", len(pins), 1)

	// ---- documents the agents and the humans follow
	check(t, "AGENTS.md: empty placeholder boxes of the preserved original become plain lines", has(agents, "empty template checkbox") && has(agents, "never counts as open boxes"), "placeholder rule missing")
	contributing := read(t, "CONTRIBUTING.md")
	check(t, "CONTRIBUTING: says what a manual run from a branch can and cannot test", has(contributing, "workflow file of the run differs"), "CONTRIBUTING lacks the limit of manual runs")
	check(t, "AGENTS.md has both sets of rules", regexp.MustCompile(`(?m)^## Elaborating new issues`).MatchString(agents) && regexp.MustCompile(`(?m)^## Implementing an issue`).MatchString(agents), "AGENTS.md sections")
	check(t, "AGENTS.md: defaults first, assumptions, at most three questions (no 'five' left)",
		has(sec, "Defaults first") && has(sec, "at most three") && has(sec, "Assumptions (reply to change any)") && !has(strings.ToLower(agents), "at most five"), "defaults-first rule")
	eq(t, "AGENTS.md: the three Next lines (ready, needs-info, proposal)", countLines(sec, regexp.MustCompile(`^   - .*\*\*Next:\*\*`)), 3)
	check(t, "AGENTS.md: proposal mode comments only", has(sec, "Proposal mode") && has(sec, "Never edit the body or the labels"), "proposal mode")
	tri := between(agents, "## Triage of new issues", "## Elaborating new issues")
	check(t, "AGENTS.md: the triage comment ends with Next and proposes no body (one comment per run)",
		has(tri, "**Next:**") && !has(tri, "skeleton comment") && !has(tri, "add one comment with a proposed"), "triage rules")
	flow := between(agents, "## The issue flow", "## Triage of new issues")
	step4 := regexp.MustCompile(`^[1-4]\. \*\*`)
	check(t, "AGENTS.md: the issue flow in four steps and the who-can-do-what table",
		countLines(flow, step4) == 4 && has(flow, "| Who | Opens an issue | Comments on a `needs-info` issue | `/elaborate` | `/implement` |"), "issue flow")
	ia := between(contributing, "## Issue agent (maintainers)", "")
	check(t, "CONTRIBUTING: four steps, who can do what, how an answer runs the agent again, /implement owner only",
		has(ia, "The flow in four steps") && countLines(ia, step4) == 4 && has(ia, "Who can do what") &&
			has(ia, "Any comment of the owner or a collaborator on an issue labelled `needs-info` runs the agent again") &&
			has(ia, "| `/implement [instructions]` | the implementation starts on a `ready` issue | refused with the reason | refused with the reason |"), "issue agent flow")
	check(t, "AGENTS.md says: never merge, Refs not Closes, and the cost rules",
		regexp.MustCompile(`(?i)never.*merge`).MatchString(agents) && has(agents, "Refs #<n>") && has(strings.ToLower(agents), "cost"), "AGENTS.md rules")
	tpl := read(t, ".github/pull_request_template.md")
	check(t, "the pull request template starts with a TLDR (what, why, how)",
		(has(tpl, "\n## TLDR") || strings.HasPrefix(tpl, "## TLDR")) && has(tpl, "**What:**") && has(tpl, "**Why:**") && has(tpl, "**How:**"), "TLDR missing")
	for _, h := range []string{"Decisions and questions", "Verification", "Not verified", "Risks and limitations"} {
		check(t, "the template has '"+h+"'", has(tpl, "\n## "+h) || strings.HasPrefix(tpl, "## "+h), "template lacks '"+h+"'")
	}
	check(t, "the TLDR comes first", strings.Index(tpl, "## TLDR") >= 0 && strings.Index(tpl, "## TLDR") < strings.Index(tpl, "## Change"), "TLDR not first")
	elab, impl := read(t, ".github/workflows/claude-elaborate.yml"), read(t, ".github/workflows/claude-implement.yml")
	for _, v := range []string{"TFPLS_AGENT_ENABLED", "TFPLS_AGENT_MODEL_ELABORATE", "TFPLS_AGENT_MODEL_IMPLEMENT", "TFPLS_AGENT_MAX_ELAB_PER_ISSUE", "TFPLS_AGENT_MAX_IMPL_PER_ISSUE", "TFPLS_AGENT_MAX_ELAB_PER_DAY", "TFPLS_AGENT_MAX_IMPL_PER_DAY"} {
		check(t, v+" is documented and used", has(contributing, v) && (has(elab, v) || has(impl, v)), v+" not both documented and used")
	}
}
