package repocheck

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const wfDir = ".github/workflows/"

var routeRe = regexp.MustCompile(`^.*/workflows/([a-z-]+)\.yml@.*$`)

func route(wf any, job string) string {
	return routeRe.ReplaceAllString(str(at(wf, "jobs", job, "uses")), "$1")
}

// Static checks of the single entry workflow .github/workflows/tofu.yml: routing, secrets, and that every input it forwards exists.
func TestEntryWorkflow(t *testing.T) {
	E := load(t, wfDir+"tofu.yml")
	jobs := keys(at(E, "jobs"))
	eq(t, "the entry workflow has the seven routes", strings.Join(jobs, " "), "apply comment dashboard drift fix locks plan")
	cond := func(j string) string { return str(at(E, "jobs", j, "if")) }
	count := func(s, sub string) int { return countLinesFixed(s, sub) }
	eq(t, "pull_request -> plan.yml", route(E, "plan")+"|"+cond("plan"), "plan|github.event_name == 'pull_request'")
	eq(t, "issue_comment on a pull request -> comment-ops.yml", route(E, "comment")+"|"+cond("comment"),
		"comment-ops|github.event_name == 'issue_comment' && github.event.action == 'created' && github.event.issue.pull_request")
	eq(t, "issue_comment on an issue and the fix task -> drift-fix.yml",
		route(E, "fix")+"|"+str(count(cond("fix"), "!github.event.issue.pull_request"))+"|"+str(count(cond("fix"), "task == 'fix'")), "drift-fix|1|1")
	eq(t, "push -> apply.yml", route(E, "apply")+"|"+cond("apply"),
		"apply|github.event_name == 'push' && github.ref == format('refs/heads/{0}', github.event.repository.default_branch)")
	eq(t, "schedule and dispatch -> drift.yml", route(E, "drift")+"|"+str(count(cond("drift"), "'schedule'")), "drift|1")
	eq(t, "locks.yml only by the manual task, never on the schedule",
		route(E, "locks")+"|"+str(count(cond("locks"), "'schedule'"))+"|"+str(count(cond("locks"), "task == 'locks'")), "locks|0|1")
	var needs []string
	for _, n := range list(at(E, "jobs", "dashboard", "needs")) {
		needs = append(needs, str(n))
	}
	eq(t, "dashboard refresh after drift", route(E, "dashboard")+"|"+strings.Join(needs, ","), "dashboard|drift")

	sec := map[string]bool{}
	for _, j := range jobs {
		sec[str(at(E, "jobs", j, "secrets"))] = true
	}
	eq(t, "every route passes the secrets on", strings.Join(uniqSorted(mapKeys(sec)), "\n"), "inherit")

	// every input forwarded to a building block must exist there, and every entry input must be used somewhere
	for _, j := range []string{"plan", "comment", "apply", "drift", "locks", "dashboard", "fix"} {
		target := route(E, j)
		tw := load(t, wfDir+target+".yml")
		var missing []string
		for _, k := range keys(at(E, "jobs", j, "with")) {
			if !has(at(tw, "on", "workflow_call", "inputs"), k) {
				missing = append(missing, k)
			}
		}
		eq(t, j+" -> "+target+".yml: every forwarded input exists there", strings.Join(missing, " "), "")
	}
	jobsJSON, _ := json.Marshal(at(E, "jobs"))
	var unused []string
	for _, k := range keys(at(E, "on", "workflow_call", "inputs")) {
		if !regexp.MustCompile(`inputs\.` + k + `\b`).Match(jobsJSON) {
			unused = append(unused, k)
		}
	}
	eq(t, "every input of the entry workflow is forwarded", strings.Join(unused, " "), "")
	eq(t, "no building block is referenced by a local path (callers are in other repositories)",
		countLines(read(t, wfDir+"tofu.yml"), regexp.MustCompile(`uses: \./`)), 0)

	// one default per switch in every workflow that has it
	for _, k := range []string{"audit", "drift_issues"} {
		var vals []string
		for _, f := range []string{"tofu", "plan", "apply", "comment-ops", "drift", "locks"} {
			// a workflow without the input is skipped; one with a default of false is not (stricter than the `// "-"` default)
			if d := at(load(t, wfDir+f+".yml"), "on", "workflow_call", "inputs", k, "default"); d != nil {
				vals = append(vals, str(d))
			}
		}
		eq(t, k+" has the same default in every workflow", strings.Join(uniqSorted(vals), " "), "true")
	}
	in := at(E, "on", "workflow_call", "inputs")
	eq(t, "the defaults of the production profile", str(at(in, "audit", "default"))+","+str(at(in, "drift_issues", "default"))+","+str(at(in, "queue", "default")), "true,true,false")

	// the approval rules and CODEOWNERS come from the default branch, not from the pull request's own config
	C := load(t, wfDir+"comment-ops.yml")
	pick := func(steps []map[string]any, sel func(map[string]any) bool, path ...string) string {
		var out []string
		for _, s := range steps {
			if sel(s) {
				out = append(out, str(at(s, path...)))
			}
		}
		return strings.Join(out, "\n")
	}
	named := func(n string) func(map[string]any) bool {
		return func(s map[string]any) bool { return s["name"] == n }
	}
	usesRe := func(re string) func(map[string]any) bool {
		r := regexp.MustCompile(re)
		return func(s map[string]any) bool { u, ok := s["uses"].(string); return ok && r.MatchString(u) }
	}
	const defBranch = "${{ github.event.repository.default_branch }}"
	parse := stepsOf(C, "parse")
	eq(t, "the approvals step reads the rules from the default branch", pick(parse, named("Approvals"), "with", "default_branch"), defBranch)
	eq(t, "the CODEOWNERS step reads CODEOWNERS from the default branch", pick(parse, named("CODEOWNERS approvals"), "with", "base"), defBranch)
	eq(t, "CODEOWNERS paths come from the approvals step (rules of main plus the pull request's)", pick(parse, named("CODEOWNERS approvals"), "with", "entries"), "${{ steps.a.outputs.owner_entries }}")
	eq(t, "the policy settings of the apply come from the approvals step", str(at(C, "jobs", "parse", "outputs", "entries")), "${{ steps.a.outputs.entries || steps.d.outputs.entries }}")
	eq(t, "/tofu unlock checks out the default branch, not the pull request (parse job)", pick(parse, usesRe("actions/checkout"), "with", "ref"),
		"${{ steps.p.outputs.cmd == 'unlock' && github.event.repository.default_branch || steps.p.outputs.sha }}")
	eq(t, "/tofu unlock runs the default branch's code (command job)", pick(stepsOf(C, "command"), usesRe("actions/prepare"), "with", "ref"),
		"${{ needs.parse.outputs.cmd == 'unlock' && github.event.repository.default_branch || needs.parse.outputs.sha }}")
	eq(t, "the triage workflow gives Claude no raw gh api", countLines(read(t, wfDir+"claude-triage.yml"), regexp.MustCompile(`allowedTools.*gh api`)), 0)
}

func mapKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The wiring of the tofu action's plan-saving inputs (the behaviour is tests/tofu-run.test.sh and internal/tofu): the action
// passes the repository's visibility and allow_unencrypted_plans, and the workflows forward them.
func TestPlanSavingWiring(t *testing.T) {
	action := read(t, "actions/tofu/action.yml")
	check(t, "the action passes the repository's visibility", strings.Contains(action, "REPO_PRIVATE: ${{ github.event.repository.private }}"), "REPO_PRIVATE not passed")
	eq(t, "the action input defaults to false", str(at(load(t, "actions/tofu/action.yml"), "inputs", "allow_unencrypted_plans", "default")), "false")
	check(t, "the action passes the input to the engine", strings.Contains(action, "ALLOW_UNENCRYPTED_PLANS: ${{ inputs.allow_unencrypted_plans }}"), "env not wired in action.yml")
	for _, f := range []string{"tofu", "plan", "comment-ops", "drift-fix"} {
		in := at(load(t, wfDir+f+".yml"), "on", "workflow_call", "inputs", "allow_unencrypted_plans")
		eq(t, f+".yml declares allow_unencrypted_plans (boolean, default false)", str(at(in, "type"))+"/"+str(at(in, "default")), "boolean/false")
	}
	const pass = "allow_unencrypted_plans: ${{ inputs.allow_unencrypted_plans }}"
	for _, f := range []string{"plan", "comment-ops", "drift-fix"} {
		eq(t, f+".yml passes it to the tofu action", countLinesFixed(read(t, wfDir+f+".yml"), pass), 1)
	}
	eq(t, "tofu.yml passes it to the three workflows that save plans", countLinesFixed(read(t, wfDir+"tofu.yml"), pass), 3)
	eq(t, "/tofu plan runs at 20 in parallel (it writes nothing shared)",
		countLinesFixed(str(at(load(t, wfDir+"comment-ops.yml"), "jobs", "command", "strategy", "max-parallel")), "&& 20"), 1)
}

// Every job in every workflow must have a timeout (a hung runner or tofu must not run for hours).
func TestJobTimeouts(t *testing.T) {
	for _, f := range glob(t, wfDir+"*.yml") {
		wf := load(t, f)
		for _, j := range keys(at(wf, "jobs")) {
			// jobs that only call a reusable workflow (uses:) cannot have a timeout themselves
			if orEmpty(at(wf, "jobs", j, "uses")) != "" {
				continue
			}
			tm := orEmpty(at(wf, "jobs", j, "timeout-minutes"))
			check(t, filepath.Base(f)+" job "+j+" has timeout-minutes ("+tm+")", tm != "", filepath.Base(f)+" job "+j+" has no timeout-minutes")
		}
	}
	for _, f := range []string{"plan", "apply", "drift", "comment-ops"} {
		d := orEmpty(at(load(t, wfDir+f+".yml"), "on", "workflow_call", "inputs", "timeout_minutes", "default"))
		check(t, f+".yml: timeout_minutes input defaults to 60", d == "60", f+".yml: timeout_minutes default ("+d+")")
	}
}
