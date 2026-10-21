// Package detect builds the job matrix of a run from .tfpls.yml (stacks x workspaces; all, changed, or selected by name or
// glob) and validates that file. It is what actions/detect-workspaces does.
package detect

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Problem is one finding of the validation: an error stops the run, a warning is shown (unknown keys are probable typos).
type Problem struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

var settings = []string{"auto_apply", "create_workspace", "var_file", "parallelism", "policy_dir", "policy_packs", "policy_params", "approvals", "notify_secret", "notify_format", "notify_mention", "notify_template", "after", "drift_remediate", "require_reviewed_plan", "plan_lock", "lock_timeout", "drift_fix"}
var stackKeys = append([]string{"name", "dir", "workspaces", "workspaces_file", "workspaces_glob", "workspace_paths", "ignore", "watch", "triggers", "overrides"}, settings...)
var topKeys = []string{"defaults", "stacks", "overrides"}

var (
	reName     = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	rePack     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	reSecret   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	reTemplate = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_./-]*$`)
	reTimeout  = regexp.MustCompile(`^[0-9]+(s|m|h)$`)
)

type obj = map[string]any

func typeOf(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "number"
}

// interp renders a value as jq string interpolation does: a string as is, anything else as JSON.
func interp(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func sortedKeys(m obj) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func has(m obj, k string) bool { _, ok := m[k]; return ok }

func contains(list []string, k string) bool {
	for _, x := range list {
		if x == k {
			return true
		}
	}
	return false
}

func normKey(s string) string {
	return strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(s))
}

type validator struct{ out []Problem }

func (v *validator) err(path, m string)  { v.out = append(v.out, Problem{"error", path + ": " + m}) }
func (v *validator) warn(path, m string) { v.out = append(v.out, Problem{"warning", path + ": " + m}) }

func (v *validator) unknown(o obj, known []string, path string) {
	for _, k := range sortedKeys(o) {
		if contains(known, k) {
			continue
		}
		hint := ""
		for _, m := range known {
			if normKey(m) == normKey(k) {
				hint = fmt.Sprintf(" (did you mean `%s`?)", m)
				break
			}
		}
		v.warn(path, fmt.Sprintf("unknown key `%s` is ignored%s", k, hint))
	}
}

func (v *validator) strlist(val any, path string) {
	l, ok := val.([]any)
	if !ok {
		v.err(path, "must be a list of strings")
		return
	}
	for _, x := range l {
		if _, ok := x.(string); !ok {
			v.err(path, "must be a list of strings")
			return
		}
	}
}

func isStr(val any, re *regexp.Regexp) bool {
	s, ok := val.(string)
	return ok && re.MatchString(s)
}

func isOneOf(val any, set ...string) bool {
	s, ok := val.(string)
	return ok && contains(set, s)
}

func (v *validator) setting(k string, val any, path string) {
	switch k {
	case "auto_apply":
		if !isOneOf(val, "never", "always", "additive_only") {
			v.err(path, "must be never, always or additive_only")
		}
	case "create_workspace":
		v.err(path, "was removed: tfpls never creates a tofu workspace. Create it once with `tofu workspace new <name>` and delete this key")
	case "require_reviewed_plan", "plan_lock", "drift_fix":
		if typeOf(val) != "boolean" {
			v.err(path, "must be true or false (without quotes)")
		}
	case "parallelism":
		n, ok := num(val)
		if !(ok && n >= 1 && n == math.Floor(n)) {
			v.err(path, "must be a whole number of 1 or more")
		}
	case "policy_params":
		m, isObj := val.(obj)
		bad := !isObj
		if isObj {
			for _, x := range m {
				if typeOf(x) != "object" {
					bad = true
				}
			}
		}
		if bad {
			v.err(path, "must map a pack name to its parameters, e.g. `safety: { max_destroys: 3 }`")
			return
		}
		for _, name := range sortedKeys(m) {
			if !rePack.MatchString(name) {
				v.err(path, fmt.Sprintf("pack names are lowercase letters, digits and `-`, got `%s`", name))
			}
		}
	case "approvals":
		v.err(path, "was removed in v0.9.0: the number of approvals comes from the repository's own settings (the pull_request rule of a ruleset on the default branch); delete this key. A repository admin can apply in an emergency with `/tofu apply <workspace> --now`")
	case "notify_secret":
		if !isStr(val, reSecret) {
			v.err(path, "must be SLACK_WEBHOOK or a key of the secret TFPLS_NOTIFY_WEBHOOKS (capital letters, digits and `_`), not a webhook URL")
		}
	case "notify_template":
		if !(isStr(val, reTemplate) && !strings.Contains(val.(string), "..")) {
			v.err(path, "must be a relative path inside the repository (no leading `/`, no `..`), e.g. .github/tfpls/slack.jq")
		}
	case "notify_format":
		if !isOneOf(val, "slack", "teams", "generic") {
			v.err(path, "must be slack, teams or generic")
		}
	case "after":
		v.strlist(val, path)
	case "drift_remediate":
		if !isOneOf(val, "never", "additive_only", "always") {
			v.err(path, "must be never, additive_only or always")
		}
	case "policy_packs":
		v.strlist(val, path)
		if l, ok := val.([]any); ok {
			for _, x := range l {
				if !isStr(x, rePack) {
					v.err(path, fmt.Sprintf("pack names are lowercase letters, digits and `-`, got `%s`", interp(x)))
				}
			}
		}
	case "lock_timeout":
		if !isStr(val, reTimeout) {
			v.err(path, "must look like 30s, 5m or 1h")
		}
	default:
		if typeOf(val) != "string" {
			v.err(path, "must be a string")
		}
	}
}

func (v *validator) settings(o obj, path string) {
	for _, k := range sortedKeys(o) {
		if contains(settings, k) {
			v.setting(k, o[k], path+"."+k)
		}
	}
}

func (v *validator) overrides(ov any, path string) {
	m, ok := ov.(obj)
	if !ok {
		v.err(path, "must be a map of workspace pattern to settings")
		return
	}
	for _, k := range sortedKeys(m) {
		p := fmt.Sprintf("%s[\"%s\"]", path, k)
		val, ok := m[k].(obj)
		if !ok {
			v.err(p, "must be a map of settings")
			continue
		}
		v.settings(val, p)
		v.unknown(val, settings, p)
	}
}

var stringKeyHints = map[string]string{
	"workspaces_file": "must be a path to a file with one workspace name per line",
	"workspaces_glob": `must be a glob such as "workspaces/*"`,
	"var_file":        `must be a path such as "env/{workspace}.tfvars"`,
	"policy_dir":      `must be a directory path such as "policies"`,
}

func (v *validator) stack(sv any, path string) {
	s, ok := sv.(obj)
	if !ok {
		v.err(path, "must be a map")
		return
	}
	if name, isStrName := s["name"].(string); !isStrName || !reName.MatchString(name) {
		v.err(path, "needs a `name` of letters, digits, `_`, `.` or `-`")
	}
	if has(s, "dir") && typeOf(s["dir"]) != "string" {
		v.err(path+".dir", "must be a string")
	}
	nsrc := 0
	for _, k := range []string{"workspaces", "workspaces_file", "workspaces_glob"} {
		if has(s, k) {
			nsrc++
		}
	}
	if nsrc == 0 {
		v.err(path, "needs one of `workspaces`, `workspaces_file` or `workspaces_glob` to say which workspaces exist")
	} else if nsrc > 1 {
		v.err(path, "set only one of `workspaces`, `workspaces_file` and `workspaces_glob` (they are three ways to say which workspaces exist)")
	}
	if has(s, "workspaces") {
		v.strlist(s["workspaces"], path+".workspaces")
	}
	for _, k := range []string{"workspaces_file", "workspaces_glob", "var_file", "policy_dir"} {
		if has(s, k) && typeOf(s[k]) != "string" {
			v.err(path+"."+k, stringKeyHints[k])
		}
	}
	for _, k := range []string{"workspace_paths", "ignore", "watch"} {
		if has(s, k) {
			v.strlist(s[k], path+"."+k)
		}
	}
	if has(s, "triggers") {
		t, ok := s["triggers"].(obj)
		if !ok {
			v.err(path+".triggers", "must map a workspace pattern to a list of workspace patterns")
		} else {
			for _, k := range sortedKeys(t) {
				v.strlist(t[k], fmt.Sprintf("%s.triggers[\"%s\"]", path, k))
			}
		}
	}
	if has(s, "overrides") {
		v.overrides(s["overrides"], path+".overrides")
	}
	v.settings(s, path)
	v.unknown(s, stackKeys, path)
}

// Validate checks a parsed .tfpls.yml (nil or false = an empty file, which is valid: the defaults apply). Errors stop the
// run, warnings are shown. The messages are the ones of the former validate.jq, in a stable order (keys sorted).
func Validate(cfg any) []Problem {
	v := &validator{out: []Problem{}}
	if cfg == nil || cfg == false {
		cfg = obj{}
	}
	c, ok := cfg.(obj)
	if !ok {
		v.err("config", "is not a map; see examples/.tfpls.yml")
		return v.out
	}
	if has(c, "workspaces") {
		v.err("workspaces", "the old top-level `workspaces` list was removed in v0.6.0: describe each entry as a stack under `stacks:` (name, dir, workspaces: [...]) or drop the file to use the default layout, see docs/migration.md")
	}
	if has(c, "overrides") {
		v.overrides(c["overrides"], "overrides")
	}
	if has(c, "defaults") {
		d, ok := c["defaults"].(obj)
		if !ok {
			v.err("defaults", "must be a map of settings")
		} else {
			v.settings(d, "defaults")
			v.unknown(d, settings, "defaults")
		}
	}
	if has(c, "stacks") {
		l, ok := c["stacks"].([]any)
		if !ok {
			v.err("stacks", "must be a list")
		} else {
			for i, s := range l {
				v.stack(s, fmt.Sprintf("stacks[%d]", i))
			}
			seen := map[string]int{}
			for _, s := range l {
				if m, ok := s.(obj); ok && m["name"] != nil {
					seen[interp(m["name"])]++
				}
			}
			dups := []string{}
			for n, c := range seen {
				if c > 1 {
					dups = append(dups, n)
				}
			}
			sort.Strings(dups)
			for _, n := range dups {
				v.err("stacks", fmt.Sprintf("the stack name `%s` is used more than once", n))
			}
		}
	}
	rest := obj{}
	for k, x := range c {
		if k != "workspaces" {
			rest[k] = x
		}
	}
	v.unknown(rest, topKeys, "config")
	return v.out
}

// HasError reports whether a problem list contains an error.
func HasError(ps []Problem) bool {
	for _, p := range ps {
		if p.Level == "error" {
			return true
		}
	}
	return false
}
