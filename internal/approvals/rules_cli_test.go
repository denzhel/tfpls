package approvals

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/denzhel/tfpls/internal/detect"
	"github.com/denzhel/tfpls/internal/ghactions"
)

// `tfpls approvals rules` end to end, in process (these were the checks of tests/policy.test.sh, which read the JSON with a JSON
// tool): the workspace rules come from the DEFAULT BRANCH config, matched by (directory, workspace); the approval counts
// come from the repository's ruleset.

func validateForTest(cfg any) (string, bool) {
	for _, p := range detect.Validate(cfg) {
		if p.Level == "error" {
			return p.Msg, false
		}
	}
	return "", true
}

type rulesRun struct {
	rc      int
	out     string            // stdout
	dry     map[string]any    // the dry-run JSON line
	outputs map[string]string // GITHUB_OUTPUT
	rules   []map[string]any  // dry.rules
}

type rulesOpts struct {
	base    string // the base config text; "\x00missing" = no file (TFPLS_BASE_CONFIG_FILE=missing)
	ruleset string // the ruleset file content ("" = [])
	dry     bool
}

func runRules(t *testing.T, heads string, o rulesOpts) rulesRun {
	t.Helper()
	dir := t.TempDir()
	rsFile := filepath.Join(dir, "rs.json")
	rs := o.ruleset
	if rs == "" {
		rs = "[]"
	}
	if err := os.WriteFile(rsFile, []byte(rs), 0o644); err != nil {
		t.Fatal(err)
	}
	baseFile := filepath.Join(dir, "base.yml")
	if o.base == "\x00missing" {
		baseFile = "missing"
	} else if err := os.WriteFile(baseFile, []byte(o.base), 0o644); err != nil {
		t.Fatal(err)
	}
	gho := filepath.Join(dir, "gho")
	env := map[string]string{
		"TFPLS_RULESET_FILE": rsFile, "TFPLS_BASE_CONFIG_FILE": baseFile, "HEAD_ENTRIES": heads,
		"CONFIG": ".tfpls.yml", "DEFAULT_BRANCH": "main", "REPO": "o/r", "GITHUB_OUTPUT": gho,
	}
	if o.dry {
		env["TFPLS_DRY"] = "1"
	}
	var stdout bytes.Buffer
	rc := Rules(Env{
		Getenv: func(k string) string { return env[k] }, Stdout: &stdout, Stderr: io.Discard,
		GH: &fakeGH{}, Validate: validateForTest,
	})
	r := rulesRun{rc: rc, out: stdout.String(), outputs: map[string]string{}}
	if b, err := os.ReadFile(gho); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if k, v, ok := strings.Cut(l, "="); ok {
				r.outputs[k] = v
			}
		}
	}
	for _, l := range strings.Split(r.out, "\n") {
		if strings.HasPrefix(l, "{") {
			_ = json.Unmarshal([]byte(l), &r.dry)
			for _, x := range r.dry["rules"].([]any) {
				r.rules = append(r.rules, x.(map[string]any))
			}
		}
	}
	return r
}

func ent(dir, workspace string, label ...string) string {
	l := workspace
	if len(label) > 0 {
		l = label[0]
	}
	b, _ := json.Marshal([]map[string]string{{"name": "x/" + workspace, "label": l, "dir": dir, "workspace": workspace}})
	return string(b)
}

// withHead adds fields to the first head entry.
func withHead(t *testing.T, heads string, add map[string]string) string {
	t.Helper()
	var l []map[string]string
	if err := json.Unmarshal([]byte(heads), &l); err != nil {
		t.Fatal(err)
	}
	for k, v := range add {
		l[0][k] = v
	}
	b, _ := json.Marshal(l)
	return string(b)
}

func need(r rulesRun) string {
	var parts []string
	for _, m := range r.rules {
		parts = append(parts, m["name"].(string)+"="+m["policy_packs"].(string))
	}
	return strings.Join(parts, ",")
}

func entryField(r rulesRun, list, field string) string {
	return r.dry[list].([]any)[0].(map[string]any)[field].(string)
}

const prodBase = `stacks:
  - name: prod
    dir: envs/prod
    workspaces: [default]
    policy_packs: [p2]
  - name: dev
    dir: envs/dev
    workspaces: [default]`

func eqs(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", name, got, want)
	}
}

func TestRulesFromTheDefaultBranch(t *testing.T) {
	dry := func(heads, base string) rulesRun { return runRules(t, heads, rulesOpts{base: base, dry: true}) }
	eqs(t, "the protected stack's own rule applies", need(dry(ent("envs/prod", "default", "prod"), prodBase)), "prod=p2")
	eqs(t, "an alias stack on a protected directory gets main's rule (any name in the pull request)", need(dry(ent("envs/prod", "default", "default"), prodBase)), "default=p2")
	eqs(t, "the same directory written another way is the same directory",
		need(dry(ent("./envs//prod/", "default"), prodBase))+"|"+need(dry(ent("envs/x/../prod", "default"), prodBase)), "default=p2|default=p2")
	eqs(t, "an unprotected directory has no stack rule", need(dry(ent("envs/dev", "default"), prodBase)), "default=")
	eqs(t, "a new directory gets the base defaults", need(dry(ent("envs/new", "default"), "defaults: { policy_packs: [p3] }\n"+prodBase)), "default=p3")
	eqs(t, "base overrides apply to a new directory by workspace name", need(dry(ent("envs/new", "prod-x"), "overrides:\n  \"prod*\": { policy_packs: [p4] }\n")), "prod-x=p4")
	def := "overrides:\n  \"prod*\": { policy_packs: [p3] }"
	eqs(t, "default-layout base: a prod workspace in the default layout is protected", need(dry(ent(".", "prod-eu"), def)), "prod-eu=p3")
	eqs(t, "default-layout base while the pull request adds a stack in another directory", need(dry(ent("services/api", "prod-eu"), def)), "prod-eu=p3")
	shared := "stacks:\n  - { name: a, dir: ., workspaces: [dev] }\n  - { name: b, dir: ., workspaces: [prod], policy_packs: [p3] }"
	eqs(t, "two base stacks sharing a directory: each workspace gets its own", need(dry(ent(".", "dev"), shared))+"|"+need(dry(ent(".", "prod"), shared)), "dev=|prod=p3")
	eqs(t, "no config on the default branch: nothing configured (the action's strict default of 1 applies)", need(dry(ent(".", "dev"), "")), "dev=")

	// the settings of the apply come from main, never from the pull request
	r := dry(withHead(t, ent("envs/dev", "default"), map[string]string{"policy_packs": ""}), "defaults: { policy_packs: [safety] }\n"+prodBase)
	eqs(t, "policy settings of the apply come from main", entryField(r, "entries", "policy_packs"), "safety")
	r = dry(withHead(t, ent("envs/prod", "default"), map[string]string{"notify_secret": "EVIL"}), "defaults: { notify_secret: OPS_HOOK }\n"+prodBase)
	eqs(t, "notify_secret comes from main too", entryField(r, "entries", "notify_secret"), "OPS_HOOK")
	r = dry(withHead(t, ent("envs/prod", "default"), map[string]string{"owner_paths": "mine/"}), prodBase)
	var owners []string
	for _, x := range r.dry["owner_entries"].([]any) {
		owners = append(owners, x.(map[string]any)["owner_paths"].(string))
	}
	eqs(t, "the owner paths include main's and the pull request's", strings.Join(owners, ","), "envs/prod,mine/")
}

func TestRulesReadingAndCheckingTheBaseConfig(t *testing.T) {
	// detect-workspaces hands over an OBJECT keyed by label (what the real action outputs); `entries` must come back the same way
	var one []map[string]string
	_ = json.Unmarshal([]byte(ent("envs/prod", "default")), &one)
	obj, _ := json.Marshal(map[string]map[string]string{one[0]["label"]: one[0]})
	r := runRules(t, string(obj), rulesOpts{base: "\x00missing"})
	var entries map[string]any
	if err := json.Unmarshal([]byte(r.outputs["entries"]), &entries); err != nil {
		t.Fatalf("entries output: %v (%q)", err, r.outputs["entries"])
	}
	var keysOf []string
	for k := range entries {
		keysOf = append(keysOf, k)
	}
	sort.Strings(keysOf)
	eqs(t, "rules takes detect's object (keyed by label) and returns entries as such an object", strings.Join(keysOf, ",")+"|"+yesArray(r.outputs["rules"]), "default|array")

	ok := runRules(t, ent("envs/prod", "default"), rulesOpts{base: prodBase + "\n", dry: true})
	eqs(t, "rules reads a base config", "rc="+itoa(ok.rc)+"|"+ok.rules[0]["policy_packs"].(string), "rc=0|p2")
	miss := runRules(t, ent("envs/prod", "default"), rulesOpts{base: "\x00missing", dry: true})
	eqs(t, "a missing base config is fine (defaults)", "rc="+itoa(miss.rc), "rc=0")
	bad := runRules(t, ent("envs/prod", "default"), rulesOpts{base: "defaults: { auto_apply: sometimes }\n", dry: true})
	eqs(t, "a base config with errors refuses", "rc="+itoa(bad.rc)+"|"+yesNo(strings.Contains(bad.out, "config on the default branch has errors")), "rc=1|yes")
	notYAML := runRules(t, ent("envs/prod", "default"), rulesOpts{base: "a: [unclosed\n", dry: true})
	eqs(t, "a base config that is not YAML refuses", "rc="+itoa(notYAML.rc), "rc=1")
}

func yesArray(s string) string {
	if strings.HasPrefix(s, "[") {
		return "array"
	}
	return "not an array: " + s
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// detect with the layout of repo, as the real action: its entries, an object keyed by label
func detectEntries(t *testing.T, repo string) []map[string]any {
	t.Helper()
	t.Chdir(repo)
	gho := filepath.Join(t.TempDir(), "out")
	env := map[string]string{"GITHUB_OUTPUT": gho, "CONFIG": ".tfpls.yml", "MODE": "all", "BASE": "", "NAME": ""}
	getenv := func(k string) string { return env[k] }
	rc := detect.Run(detect.Env{
		Getenv: getenv, Runner: &ghactions.Runner{Getenv: getenv, Stdout: io.Discard},
		Git: func(args ...string) ([]byte, error) { return exec.Command("git", args...).Output() },
	})
	if rc != 0 {
		t.Fatalf("detect rc=%d", rc)
	}
	b, err := os.ReadFile(gho)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]map[string]any
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "entries="); ok {
			if err := json.Unmarshal([]byte(v), &obj); err != nil {
				t.Fatal(err)
			}
		}
	}
	var labels []string
	for k := range obj {
		labels = append(labels, k)
	}
	sort.Strings(labels)
	var out []map[string]any
	for _, k := range labels {
		out = append(out, obj[k])
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// when the pull request changes nothing, the rules equal what detect-workspaces computes
func TestRulesEqualDetectWhenTheHeadChangesNothing(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "workspaces/dev-eu/a.tf"), "")
	writeFile(t, filepath.Join(repo, "workspaces/prod-eu/a.tf"), "")
	cfg := "defaults: { policy_packs: [p1] }\noverrides:\n  \"prod-*\": { policy_packs: [p2] }\n"
	writeFile(t, filepath.Join(repo, ".tfpls.yml"), cfg)
	heads := detectEntries(t, repo)
	eqs(t, "the invariant check has two workspaces to compare (it was vacuous when detect refused the config)", itoa(len(heads)), "2")
	hb, _ := json.Marshal(heads)
	r := runRules(t, string(hb), rulesOpts{base: cfg, dry: true})
	var a, b []string
	for _, m := range r.rules {
		a = append(a, m["name"].(string)+"|"+m["policy_packs"].(string)+"|"+m["owner_paths"].(string))
	}
	for _, m := range heads {
		b = append(b, m["label"].(string)+"|"+m["policy_packs"].(string)+"|"+m["owner_paths"].(string))
	}
	sort.Strings(a)
	sort.Strings(b)
	eqs(t, "head == base: the rules equal detect's policy settings and owner paths", strings.Join(a, ";"), strings.Join(b, ";"))
}

// two patterns match one workspace and set the same key: the later one in the file wins, in approvals and in detect alike
func TestLaterPatternWinsInApprovalsAndDetect(t *testing.T) {
	later := "overrides:\n  \"prod*\": { policy_packs: [late] }\n  \"*-eu\": { policy_packs: [early] }\n"
	r := runRules(t, ent(".", "prod-eu"), rulesOpts{base: later, dry: true})
	eqs(t, "approvals: the later matching override pattern in the file wins", need(r), "prod-eu=early")
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "workspaces/prod-eu/a.tf"), "")
	writeFile(t, filepath.Join(repo, ".tfpls.yml"), later)
	eqs(t, "detect: the same pattern wins (both agree)", detectEntries(t, repo)[0]["policy_packs"].(string), "early")
}

func TestRulesetCounts(t *testing.T) {
	cnt := func(rs string) string {
		r := runRules(t, ent("envs/prod", "default"), rulesOpts{base: prodBase + "\n", ruleset: rs, dry: true})
		return r.rules[0]["approvals_mode"].(string) + "," + r.rules[0]["approvals_required"].(string)
	}
	rs2 := `[{"type":"pull_request","parameters":{"required_approving_review_count":2,"dismiss_stale_reviews_on_push":true}}]`
	rs0 := `[{"type":"pull_request","parameters":{"required_approving_review_count":0}}]`
	rs3 := `[{"type":"pull_request","parameters":{"required_approving_review_count":1,"dismiss_stale_reviews_on_push":false}},{"type":"pull_request","parameters":{"required_approving_review_count":3,"dismiss_stale_reviews_on_push":true,"require_code_owner_review":true,"require_last_push_approval":true}}]`
	rsOther := `[{"type":"deletion"},{"type":"required_status_checks"}]`
	eqs(t, "a ruleset with 2 approvals and dismiss-stale: strict, 2", cnt(rs2), "strict,2")
	eqs(t, "a ruleset with 0 approvals: nobody has to approve", cnt(rs0), "basic,0")
	eqs(t, "several pull request rules: the strictest count, strict when any dismisses stale approvals", cnt(rs3), "strict,3")
	eqs(t, "no pull request rule: one approval of the current commit", cnt(rsOther)+"|"+cnt("[]"), "strict,1|strict,1")
	flags := func(rs string) string {
		r := runRules(t, ent("envs/prod", "default"), rulesOpts{base: prodBase + "\n", ruleset: rs})
		return r.outputs["codeowners"] + "," + r.outputs["last_push"]
	}
	eqs(t, "code owner review and last-push approval are passed on", flags(rs3)+"|"+flags(rs2), "true,true|false,false")
	eqs(t, "an unreadable ruleset refuses", "rc="+itoa(runRules(t, ent("envs/prod", "default"), rulesOpts{base: prodBase + "\n", ruleset: "not json", dry: true}).rc), "rc=1")
}
