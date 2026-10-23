package repocheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The wiring of the workflows and composite actions that the shell tests used to read with a YAML tool.

const binAction = "denzhel/tfpls/actions/bin@main"

// stepByID returns the step of a job with the id, or nil.
func stepByID(wf any, job, id string) map[string]any {
	for _, s := range stepsOf(wf, job) {
		if s["id"] == id {
			return s
		}
	}
	return nil
}

func actionSteps(a any) []map[string]any {
	var out []map[string]any
	for _, s := range list(at(a, "runs", "steps")) {
		if m, ok := s.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func TestActionsInstallTheBinaryThenRun(t *testing.T) {
	for _, c := range []struct{ action, run string }{
		{"queue", "tfpls queue"},
		{"wait-for", "tfpls wait-for"},
		{"record", "tfpls record"},
		{"result", "tfpls result"},
		{"detect-workspaces", "tfpls detect"},
	} {
		s := actionSteps(load(t, "actions/"+c.action+"/action.yml"))
		if len(s) < 2 {
			t.Errorf("%s: fewer than two steps", c.action)
			continue
		}
		eq(t, c.action+" installs the binary first", str(s[0]["uses"]), binAction)
		eq(t, c.action+" then runs the command", str(s[1]["run"]), c.run)
	}
	s := actionSteps(load(t, "actions/approvals/action.yml"))
	eq(t, "approvals: binary, rules, enforce", str(s[0]["uses"])+"|"+str(s[1]["run"])+"|"+str(s[2]["run"]),
		binAction+"|tfpls approvals rules|tfpls approvals enforce")
}

func TestForkGuardWiring(t *testing.T) {
	plan := load(t, ".github/workflows/plan.yml")
	eq(t, "plan.yml runs the fork guard command", str(stepByID(plan, "detect", "fork")["run"]), "tfpls glue fork-check")
	eq(t, "detection is skipped for forks", str(stepByID(plan, "detect", "d")["if"]), "steps.fork.outputs.is_fork != 'true'")
	eq(t, "plan.yml gives the detect step the pull request's own head",
		str(at(stepByID(plan, "detect", "d"), "with", "head_ref")), "${{ github.event.pull_request.head.sha }}")
	eq(t, "plan.yml adds no result job by default", orNone(at(plan, "jobs", "result")), "none")
	eq(t, "the jobs that only read have no write (plan detect)", str(at(plan, "jobs", "detect", "permissions", "contents")), "read")
	eq(t, "plan jobs cannot write the data branch", str(at(plan, "jobs", "plan", "permissions", "contents")), "read")
}

func orNone(v any) string {
	if v == nil {
		return "none"
	}
	return str(v)
}

func TestCommentOpsWiring(t *testing.T) {
	cw := load(t, ".github/workflows/comment-ops.yml")
	eq(t, "comment-ops.yml installs the binary, then runs tfpls comment parse",
		str(at(stepsOf(cw, "parse")[0], "uses"))+"|"+str(stepByID(cw, "parse", "p")["run"]), binAction+"|tfpls comment parse")
	eq(t, "the parse job only reads", str(at(cw, "jobs", "parse", "permissions", "contents")), "read")
	var nomatch string
	for _, s := range stepsOf(cw, "parse") {
		if s["name"] == "tell the user when nothing matched" {
			nomatch = str(s["run"])
		}
	}
	eq(t, "the nothing-matched step is tfpls comment nomatch", nomatch, "tfpls comment nomatch")

	// no step of a workflow or action calls a JSON command line tool (gh's own flag for that is fine)
	call := regexp.MustCompile(`(^|[|;&(]|\$\()[[:space:]]*` + "j" + `q[[:space:]]`)
	files := append(glob(t, ".github/workflows/*.yml"), glob(t, "actions/*/action.yml")...)
	for _, f := range files {
		var walk func(v any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				if run, ok := x["run"].(string); ok {
					for _, l := range strings.Split(run, "\n") {
						if call.MatchString(l) {
							t.Errorf("%s invokes a JSON command line tool: %s", f, l)
						}
					}
				}
				for _, c := range x {
					walk(c)
				}
			case []any:
				for _, c := range x {
					walk(c)
				}
			}
		}
		walk(load(t, f))
	}

	// dashboard, drift, drift-fix and apply run one tfpls glue command each
	var glue []string
	for _, f := range []string{"dashboard", "drift", "drift-fix", "apply"} {
		wf := load(t, ".github/workflows/"+f+".yml")
		jobs, _ := at(wf, "jobs").(map[string]any)
		for _, name := range keys(jobs) {
			for _, s := range stepsOf(wf, name) {
				if r, ok := s["run"].(string); ok && strings.HasPrefix(r, "tfpls glue ") {
					glue = append(glue, r)
				}
			}
		}
	}
	// the shell test listed them per file, jobs in the file's own order; here per file with jobs sorted
	eq(t, "dashboard, drift, drift-fix and apply run one tfpls glue command each", strings.Join(sortedStrings(glue), ","),
		strings.Join(sortedStrings([]string{"tfpls glue store-page", "tfpls glue drift-state", "tfpls glue drift-issue", "tfpls glue merged-pr"}), ","))
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestToolVersionsWorkflow(t *testing.T) {
	w := load(t, ".github/workflows/tool-versions.yml")
	eq(t, "triggers: a weekly schedule and workflow_dispatch",
		strings.Join(keys(at(w, "on")), ",")+"|"+str(len(list(at(w, "on", "schedule")))), "schedule,workflow_dispatch|1")
	perms, _ := at(w, "permissions").(map[string]any)
	var got []string
	for _, k := range keys(perms) {
		got = append(got, k+":"+str(perms[k]))
	}
	eq(t, "permissions: contents read and issues write, nothing else", strings.Join(got, ","), "contents:read,issues:write")
	n := 0
	jobs, _ := at(w, "jobs").(map[string]any)
	for _, j := range jobs {
		if has(j, "permissions") {
			n++
		}
	}
	eq(t, "no job widens them", n, 0)
}

func TestLeastPrivilege(t *testing.T) {
	level := func(v string) int {
		switch v {
		case "write":
			return 2
		case "read":
			return 1
		}
		return 0
	}
	// the grant the caller written by tools/onboard gives (a job asking for more would stop the whole run at startup)
	grant := func(k string) string {
		switch k {
		case "contents", "pull-requests", "issues", "id-token":
			return "write"
		case "actions":
			return "read"
		}
		return "none"
	}
	for _, f := range []string{"plan", "apply", "comment-ops", "drift", "drift-fix", "locks"} {
		wf := load(t, ".github/workflows/"+f+".yml")
		jobs, _ := at(wf, "jobs").(map[string]any)
		for _, job := range keys(jobs) {
			perms, _ := at(jobs[job], "permissions").(map[string]any)
			t.Run(f+"/"+job, func(t *testing.T) {
				if len(perms) == 0 {
					t.Fatal("no permissions: block")
				}
				for k, v := range perms {
					if level(str(v)) > level(grant(k)) {
						t.Errorf("asks for more than the caller grants: %s:%s", k, str(v))
					}
				}
			})
		}
	}
}

func TestMergeCommentWiring(t *testing.T) {
	a := load(t, ".github/workflows/apply.yml")
	eq(t, "detect exposes the merged PR number", str(at(a, "jobs", "detect", "outputs", "pr_number")), "${{ steps.pr.outputs.pr_number }}")
	eq(t, "the apply step hands the PR number to the tofu action", str(at(stepByID(a, "apply", "t"), "with", "pr_number")), "${{ needs.detect.outputs.pr_number }}")
	eq(t, "the apply job may write the comment", str(at(a, "jobs", "apply", "permissions", "pull-requests")), "write")
	eq(t, "the detect job still only reads", str(at(a, "jobs", "detect", "permissions", "pull-requests")), "read")
	eq(t, "apply.yml runs tfpls glue merged-pr", str(stepByID(a, "detect", "pr")["run"]), "tfpls glue merged-pr")
}

func TestFinishWebhookWiring(t *testing.T) {
	f := load(t, "actions/finish/action.yml")
	steps := actionSteps(f)
	var hook, post map[string]any
	for _, s := range steps {
		if s["id"] == "hook" {
			hook = s
		}
		if u, _ := s["uses"].(string); strings.Contains(u, "actions/notify-slack@") {
			post = s
		}
	}
	env, _ := hook["env"].(map[string]any)
	eq(t, "finish reads the key from the entry and the two secrets from its inputs",
		strings.Join([]string{str(env["KEY"]), str(env["DEFAULT_HOOK"]), str(env["HOOKS"]), str(hook["run"])}, "|"),
		"${{ fromJSON(inputs.entry).notify_secret }}|${{ inputs.webhook }}|${{ inputs.webhooks }}|tfpls finish webhook")
	eq(t, "notify-slack posts to the picked webhook", str(at(post, "with", "webhook")), "${{ steps.hook.outputs.url }}")
	eq(t, "the binary is installed first", str(steps[0]["uses"]), binAction)
	var ids []string
	for _, s := range steps[1:3] {
		switch {
		case s["id"] != nil:
			ids = append(ids, str(s["id"]))
		case s["uses"] != nil:
			ids = append(ids, str(s["uses"]))
		default:
			ids = append(ids, "-")
		}
	}
	eq(t, "the webhook is picked next, and only when there is something to send",
		strings.Join(ids, " ")+"|"+str(steps[1]["if"]), "hook denzhel/tfpls/actions/notify-slack@main|always() && inputs.notify == 'true'")
}

func TestCleanupSteps(t *testing.T) {
	for _, a := range []string{"tofu", "unlock"} {
		s := actionSteps(load(t, "actions/"+a+"/action.yml"))
		eq(t, "actions/"+a+" ends with an always() cleanup step", str(s[len(s)-1]["if"]), "always()")
	}
	s := actionSteps(load(t, "actions/tofu/action.yml"))
	last := s[len(s)-1]
	dir := str(at(last, "env", "DIR"))
	check(t, "the cleanup step falls back to the directory of the entry",
		strings.Contains(dir, "fromJSON(inputs.entry") && strings.Contains(dir, ".dir"), "cleanup DIR does not use the entry: "+dir)

	// run the step: it removes the plan files and .terraform and keeps the code
	tmp := t.TempDir()
	d := filepath.Join(tmp, "repo2", "envs", "dev")
	if err := os.MkdirAll(filepath.Join(d, ".terraform"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"tfplan", "show.txt", "plan.json", "main.tf"} {
		if err := os.WriteFile(filepath.Join(d, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(tmp, "step.sh")
	if err := os.WriteFile(script, []byte(str(last["run"])+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(dirEnv string) error {
		c := exec.Command("bash", script)
		c.Env = append(os.Environ(), "REPO_ROOT="+filepath.Join(tmp, "repo2"), "DIR="+dirEnv)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Logf("%s", out)
		}
		return err
	}
	if err := run("envs/dev"); err != nil {
		t.Fatalf("cleanup step failed: %v", err)
	}
	ents, _ := os.ReadDir(d)
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	eq(t, "the step removes the plan files (including show.txt) and .terraform, and keeps the code", strings.Join(names, " "), "main.tf")
	if err := run(""); err != nil {
		t.Errorf("an empty directory is not tolerated: %v", err)
	}
}
