package detect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/denzhel/tfpls/internal/ghactions"
	"github.com/denzhel/tfpls/internal/yamlconf"
)

// Env is what Run needs from the outside, so that it can be tested: the environment, the runner files, and git.
type Env struct {
	Getenv func(string) string
	Runner *ghactions.Runner
	Git    func(args ...string) ([]byte, error) // runs git in the current directory
}

// DefaultEnv is the real environment.
func DefaultEnv() Env {
	return Env{Getenv: os.Getenv, Runner: ghactions.New(), Git: func(args ...string) ([]byte, error) {
		return exec.Command("git", args...).Output()
	}}
}

// Entry is one selected workspace: what the jobs look up by label in the `entries` output.
type Entry struct {
	Name                string `json:"name"`
	Stack               string `json:"stack"`
	Dir                 string `json:"dir"`
	Workspace           string `json:"workspace"`
	AutoApply           string `json:"auto_apply"`
	DriftRemediate      string `json:"drift_remediate"`
	OwnerPaths          string `json:"owner_paths"`
	PolicyParams        string `json:"policy_params"`
	NotifySecret        string `json:"notify_secret"`
	NotifyFormat        string `json:"notify_format"`
	NotifyMention       string `json:"notify_mention"`
	NotifyTemplate      string `json:"notify_template"`
	PolicyDir           string `json:"policy_dir"`
	PolicyPacks         string `json:"policy_packs"`
	VarFile             string `json:"var_file"`
	Parallelism         string `json:"parallelism"`
	PlanLock            string `json:"plan_lock"`
	DriftFix            string `json:"drift_fix"`
	LockTimeout         string `json:"lock_timeout"`
	RequireReviewedPlan string `json:"require_reviewed_plan"`
	Label               string `json:"label"`
	WaitFor             string `json:"wait_for"`

	after []string
	preds []string
}

type kv struct {
	key string
	val obj
}

type stack struct {
	name           string
	raw            obj
	dir            string
	wslist         []string
	workspacePaths []string
	ignore, watch  []string
	triggers       map[string][]string
	overrides      []kv
}

func compact(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "null"
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func strs(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, x := range l {
		out = append(out, interp(x))
	}
	return out
}

// truthy is jq's notion for `//`: null and false count as absent.
func truthy(v any) bool { return v != nil && v != false }

func orStr(v any, def string) string {
	if !truthy(v) {
		return def
	}
	return interp(v)
}

func boolStr(v any, def bool) string {
	if v == nil {
		return strconv.FormatBool(def)
	}
	return interp(v)
}

func numStr(v any, def string) string {
	if !truthy(v) {
		return def
	}
	if f, ok := num(v); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return interp(v)
}

func subWorkspace(s, w string) string { return strings.ReplaceAll(s, "{workspace}", w) }

// orderedOverrides lists an overrides map in document order (the later pattern wins when several match one workspace).
func orderedOverrides(m obj, keys []string) []kv {
	out := []kv{}
	seen := map[string]bool{}
	for _, k := range keys {
		if v, ok := m[k].(obj); ok && !seen[k] {
			out = append(out, kv{k, v})
			seen[k] = true
		}
	}
	for _, k := range sortedKeys(m) {
		if v, ok := m[k].(obj); ok && !seen[k] {
			out = append(out, kv{k, v})
		}
	}
	return out
}

// mergeOverrides is jq's `$top + $own`: the keys of $top first, a key of $own replaces the value in place, new keys follow.
func mergeOverrides(top, own []kv) []kv {
	out := append([]kv{}, top...)
	for _, o := range own {
		replaced := false
		for i := range out {
			if out[i].key == o.key {
				out[i].val = o.val
				replaced = true
			}
		}
		if !replaced {
			out = append(out, o)
		}
	}
	return out
}

func merged(ms ...obj) obj {
	out := obj{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func readLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

// globDirs returns the base names of the directories the glob matches (sorted; names with a leading dot only when the
// glob asks for them, as the shell does).
func globDirs(pattern string) []string {
	pattern = strings.TrimRight(pattern, "/")
	out := []string{}
	if pattern == "" {
		return out
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return out
	}
	dotted := strings.HasPrefix(filepath.Base(pattern), ".")
	for _, p := range matches {
		base := filepath.Base(p)
		if strings.HasPrefix(base, ".") && !dotted {
			continue
		}
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			out = append(out, base)
		}
	}
	return out
}

func under(f, dir string) bool {
	if dir == "." || dir == "" {
		return true
	}
	return f == dir || strings.HasPrefix(f, dir+"/")
}

func anyGlob(globs []string, s string) bool {
	for _, g := range globs {
		if GlobMatch(g, s) {
			return true
		}
	}
	return false
}

func (s *stack) relevant(changed []string) []string {
	out := []string{}
	for _, f := range changed {
		in := under(f, s.dir)
		for _, w := range s.watch {
			if under(f, w) {
				in = true
			}
		}
		if in && !anyGlob(s.ignore, f) {
			out = append(out, f)
		}
	}
	return out
}

func (s *stack) selectChanged(changed []string, haveChanged bool) []string {
	if !haveChanged {
		return s.wslist
	}
	rel := s.relevant(changed)
	if len(rel) == 0 {
		return []string{}
	}
	if len(s.workspacePaths) == 0 {
		return s.wslist
	}
	owns := func(w, f string) bool {
		for _, p := range s.workspacePaths {
			if strings.HasPrefix(f, subWorkspace(p, w)) {
				return true
			}
		}
		return false
	}
	for _, f := range rel {
		shared := true
		for _, w := range s.wslist {
			if owns(w, f) {
				shared = false
				break
			}
		}
		if shared {
			return s.wslist
		}
	}
	out := []string{}
	for _, w := range s.wslist {
		for _, f := range rel {
			if owns(w, f) {
				out = append(out, w)
				break
			}
		}
	}
	return out
}

// expand: when a workspace is selected by a change, the workspaces its `triggers` list are selected too (transitively).
func (s *stack) expand(sel []string) []string {
	if len(s.triggers) == 0 || len(s.wslist) == 0 {
		return sel
	}
	set := map[string]bool{}
	for _, w := range sel {
		set[w] = true
	}
	for {
		added := false
		for k, vs := range s.triggers {
			fire := false
			for w := range set {
				if GlobMatch(k, w) {
					fire = true
					break
				}
			}
			if !fire {
				continue
			}
			for _, p := range vs {
				for _, w := range s.wslist {
					if !set[w] && GlobMatch(p, w) {
						set[w] = true
						added = true
					}
				}
			}
		}
		if !added {
			break
		}
	}
	out := []string{}
	for _, w := range s.wslist {
		if set[w] {
			out = append(out, w)
		}
	}
	return out
}

func (s *stack) selected(changed []string, haveChanged bool) []string {
	sel := s.selectChanged(changed, haveChanged)
	if !haveChanged || len(sel) == 0 {
		return sel
	}
	return s.expand(sel)
}

func (s *stack) entries(defaults obj, changed []string, haveChanged bool) []*Entry {
	var out []*Entry
	for _, w := range s.selected(changed, haveChanged) {
		o := obj{}
		for _, ov := range s.overrides {
			if GlobMatch(ov.key, w) || GlobMatch(ov.key, s.name) {
				o = merged(o, ov.val)
			}
		}
		m := merged(defaults, s.raw, o)
		dir := orStr(m["dir"], ".")
		var paths []string
		if truthy(m["workspace_paths"]) {
			paths = strs(m["workspace_paths"])
		} else {
			paths = []string{dir}
		}
		for i := range paths {
			paths[i] = subWorkspace(paths[i], w)
		}
		pp := ""
		if p, ok := m["policy_params"].(obj); ok && len(p) > 0 {
			pp = compact(p)
		}
		packs := ""
		if l, ok := m["policy_packs"].([]any); ok {
			packs = strings.Join(strs(l), ",")
		} else if truthy(m["policy_packs"]) {
			packs = interp(m["policy_packs"])
		}
		e := &Entry{
			Name: s.name + "/" + w, Stack: s.name, Dir: dir, Workspace: w,
			AutoApply:           orStr(m["auto_apply"], "never"),
			DriftRemediate:      orStr(m["drift_remediate"], ""),
			OwnerPaths:          strings.Join(paths, "\n"),
			PolicyParams:        pp,
			NotifySecret:        orStr(m["notify_secret"], ""),
			NotifyFormat:        orStr(m["notify_format"], "slack"),
			NotifyMention:       orStr(m["notify_mention"], ""),
			NotifyTemplate:      orStr(m["notify_template"], ""),
			PolicyDir:           orStr(m["policy_dir"], ""),
			PolicyPacks:         packs,
			VarFile:             subWorkspace(orStr(m["var_file"], ""), w),
			Parallelism:         numStr(m["parallelism"], "10"),
			PlanLock:            boolStr(m["plan_lock"], false),
			DriftFix:            boolStr(m["drift_fix"], true),
			LockTimeout:         orStr(m["lock_timeout"], "2m"),
			RequireReviewedPlan: boolStr(orFalse(m["require_reviewed_plan"]), false),
			after:               strs(m["after"]),
		}
		out = append(out, e)
	}
	return out
}

func orFalse(v any) any {
	if !truthy(v) {
		return false
	}
	return v
}

// normalize applies the layout rules: the default stack (one workspace per directory of workspaces/*) without `stacks`;
// top-level `overrides` apply to every stack (a stack's own overrides win); each stack's workspace list is resolved.
func normalize(cfg obj, order map[string][]string) (defaults obj, stacks []*stack, err error) {
	defaults, _ = cfg["defaults"].(obj)
	if defaults == nil {
		defaults = obj{}
	}
	topMap, _ := cfg["overrides"].(obj)
	top := orderedOverrides(topMap, order["overrides"])
	var raws []obj
	var owns [][]kv
	if l, ok := cfg["stacks"].([]any); ok && truthy(cfg["stacks"]) {
		for i, s := range l {
			sm, _ := s.(obj)
			raws = append(raws, sm)
			own, _ := sm["overrides"].(obj)
			owns = append(owns, orderedOverrides(own, order[fmt.Sprintf("stacks/%d/overrides", i)]))
		}
	} else {
		raws = []obj{{
			"name": "terraform", "dir": ".", "workspaces_glob": "workspaces/*",
			"workspace_paths": []any{"workspaces/{workspace}/"},
			"ignore":          []any{"**/*.md", "docs/**", ".github/**", "LICENSE*", ".gitignore"},
		}}
		owns = [][]kv{nil}
	}
	for i, raw := range raws {
		s := &stack{raw: raw, name: interp(raw["name"]), dir: ".", overrides: mergeOverrides(top, owns[i]),
			workspacePaths: strs(raw["workspace_paths"]), ignore: strs(raw["ignore"]), watch: strs(raw["watch"]),
			triggers: map[string][]string{}}
		if d, ok := raw["dir"].(string); ok && d != "" {
			s.dir = d
		}
		if t, ok := raw["triggers"].(obj); ok {
			for k, v := range t {
				s.triggers[k] = strs(v)
			}
		}
		f, _ := raw["workspaces_file"].(string)
		g, _ := raw["workspaces_glob"].(string)
		switch {
		case f != "":
			s.wslist, err = readLines(f)
			if err != nil {
				return nil, nil, fmt.Errorf("stack %s: cannot read workspaces_file %s: %v", s.name, f, err)
			}
		case g != "":
			s.wslist = globDirs(g)
		default:
			s.wslist = strs(raw["workspaces"])
		}
		stacks = append(stacks, s)
	}
	return defaults, stacks, nil
}

// unmatched lists the overrides that match no workspace and no stack name (probably a typo or a pattern for another layout).
func unmatched(stacks []*stack) []string {
	var names []string
	set := map[string]bool{}
	for _, s := range stacks {
		names = append(names, s.name)
		names = append(names, s.wslist...)
		for _, o := range s.overrides {
			set[o.key] = true
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		found := false
		for _, n := range names {
			if GlobMatch(k, n) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, k)
		}
	}
	return out
}

func label(e *Entry, count map[string]int) string {
	switch {
	case e.Workspace == "":
		return e.Name
	case e.Workspace == "default":
		return e.Stack
	case count[e.Workspace] > 1:
		return e.Name
	}
	return e.Workspace
}

// order resolves `after`: every workspace comes after its predecessors of THIS run, cycles are an error.
func order(es []*Entry) ([]*Entry, error) {
	for _, e := range es {
		e.preds = nil
		for _, o := range es {
			if o.Name == e.Name {
				continue
			}
			for _, p := range e.after {
				if GlobMatch(p, o.Workspace) || GlobMatch(p, o.Name) || GlobMatch(p, o.Label) {
					e.preds = append(e.preds, o.Label)
					break
				}
			}
		}
	}
	n := len(es)
	level := map[string]int{}
	for _, e := range es {
		level[e.Label] = 0
	}
	for i := 0; i <= n; i++ { // one pass per round on the levels of the round before: a cycle never settles
		next := map[string]int{}
		for k, v := range level {
			next[k] = v
		}
		for _, e := range es {
			m := 0
			for _, p := range e.preds {
				if level[p]+1 > m {
					m = level[p] + 1
				}
			}
			next[e.Label] = m
		}
		level = next
	}
	max := 0
	for _, v := range level {
		if v > max {
			max = v
		}
	}
	if max > n {
		var in []string
		for _, e := range es {
			if len(e.preds) > 0 {
				in = append(in, e.Label)
			}
		}
		return nil, fmt.Errorf("`after` has a cycle between workspaces: %s", strings.Join(in, ", "))
	}
	out := append([]*Entry{}, es...)
	sort.SliceStable(out, func(i, j int) bool { return level[out[i].Label] < level[out[j].Label] })
	for _, e := range out {
		e.WaitFor = strings.Join(e.preds, ",")
	}
	return out, nil
}

func fail(r *ghactions.Runner, msg string) int {
	r.Error(msg)
	return 1
}

var reAllZero = regexp.MustCompile(`^0+$`)

// Run is `tfpls detect`: it reads CONFIG, MODE, BASE, HEAD_REF, NAME (and TFPLS_MAX_MATRIX), prints annotations, and writes
// the outputs matrix, entries, names, any and configured. It returns the exit code.
func Run(e Env) int {
	r := e.Runner
	config := e.Getenv("CONFIG")
	if config == "" {
		config = ".tfpls.yml"
	}
	var cfg any
	var keyOrder map[string][]string
	if fi, err := os.Stat(config); err == nil && fi.Mode().IsRegular() {
		data, err := os.ReadFile(config)
		if err != nil {
			return fail(r, fmt.Sprintf("cannot read %s: %v", config, err))
		}
		if cfg, err = yamlconf.Parse(data); err != nil {
			return fail(r, fmt.Sprintf("%s is not valid YAML: %v", config, err))
		}
		if keyOrder, err = yamlconf.KeyOrder(data); err != nil {
			return fail(r, fmt.Sprintf("%s is not valid YAML: %v", config, err))
		}
	} else {
		r.Notice(fmt.Sprintf("no %s: using the defaults (one workspace per directory of workspaces/*, nothing auto-applies)", config))
	}
	if !truthy(cfg) {
		cfg = obj{}
	}

	// config problems with their path: errors stop the run, warnings (unknown keys = probable typos) are shown
	problems := Validate(cfg)
	for _, p := range problems {
		if p.Level == "error" {
			r.Error(p.Msg)
		} else {
			r.Warning(p.Msg)
		}
	}
	if HasError(problems) {
		return fail(r, config+" has errors (see above); the format is described in the README and examples/.tfpls.yml")
	}
	cm := cfg.(obj) // Validate refused everything else

	defaults, stacks, err := normalize(cm, keyOrder)
	if err != nil {
		return fail(r, err.Error())
	}
	configured := 0
	count := map[string]int{}
	for _, s := range stacks {
		for _, w := range s.wslist {
			// names end up in commands and file names
			if !reName.MatchString(w) || w == "." || w == ".." {
				return fail(r, "workspace names must match [A-Za-z0-9_.-]+ (and not be . or ..)")
			}
			configured++
			count[w]++
		}
	}

	var changed []string
	haveChanged := false
	name := e.Getenv("NAME")
	base, head := e.Getenv("BASE"), e.Getenv("HEAD_REF")
	if strings.HasPrefix(base, "-") || strings.HasPrefix(head, "-") {
		return fail(r, "base_ref and head_ref must be a commit, not an option")
	}
	if name == "" && e.Getenv("MODE") != "all" && base != "" && !reAllZero.MatchString(base) {
		if _, err := e.Git("cat-file", "-e", base+"^{commit}"); err == nil {
			if head == "" {
				head = "HEAD"
			}
			out, err := e.Git("diff", "--name-only", "-z", "--no-renames", base+"..."+head, "--")
			if err != nil {
				return fail(r, fmt.Sprintf("git diff %s...%s failed: %v", base, head, err))
			}
			changed = []string{}
			for _, f := range strings.Split(string(out), "\x00") {
				if f != "" {
					changed = append(changed, f)
				}
			}
			haveChanged = true
		}
	}

	// warnings that help a newcomer: no workspace at all, or an override that matches nothing
	if configured == 0 {
		r.Warning(fmt.Sprintf("no workspaces found: the default layout is one folder per workspace under workspaces/ ; describe another layout with stacks: in %s (see the README)", config))
		_ = r.Summary(fmt.Sprintf("### No workspaces found\n\nThe default layout is one folder per workspace under `workspaces/`. Describe another layout with `stacks:` in `%s`.\n", config))
	}
	for _, k := range unmatched(stacks) {
		r.Warning(fmt.Sprintf("override %q matches no workspace and no stack name (check the spelling)", k))
	}

	var es []*Entry
	for _, s := range stacks {
		es = append(es, s.entries(defaults, changed, haveChanged)...)
	}
	for _, en := range es {
		en.Label = label(en, count)
	}
	if selectors := strings.Fields(name); len(selectors) > 0 {
		var kept []*Entry
		for _, en := range es {
			for _, p := range selectors {
				if p == "all" || GlobMatch(p, en.Name) || GlobMatch(p, en.Workspace) || GlobMatch(p, en.Label) {
					kept = append(kept, en)
					break
				}
			}
		}
		es = kept
	}
	es, err = order(es)
	if err != nil {
		return fail(r, err.Error())
	}

	for _, en := range es {
		switch en.AutoApply {
		case "never", "always", "additive_only":
		default:
			return fail(r, "auto_apply must be never|always|additive_only")
		}
		if !reTimeout.MatchString(en.LockTimeout) {
			return fail(r, "lock_timeout must look like 30s, 5m or 1h")
		}
		if en.PlanLock != "true" && en.PlanLock != "false" {
			return fail(r, "plan_lock must be true or false")
		}
	}
	// GitHub allows at most 256 jobs in one matrix: fail early with advice instead of an obscure workflow error
	maxMatrix := 256
	if v := e.Getenv("TFPLS_MAX_MATRIX"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fail(r, "TFPLS_MAX_MATRIX must be a positive whole number")
		}
		maxMatrix = n
	}
	if len(es) > maxMatrix {
		return fail(r, fmt.Sprintf("this change selects %d workspaces, but GitHub allows at most %d jobs in one matrix. Split the change into smaller pull requests, or plan in batches with /tofu plan <glob>.", len(es), maxMatrix))
	}
	names, labels := map[string]bool{}, map[string]bool{}
	for _, en := range es {
		if names[en.Name] {
			return fail(r, "duplicate stack/workspace names")
		}
		names[en.Name] = true
	}
	for _, en := range es {
		if labels[en.Label] {
			return fail(r, "duplicate job labels")
		}
		labels[en.Label] = true
	}

	// The matrix has ONE key (label), so a job needs no custom name; the other fields are looked up from `entries` by label:
	// fromJSON(needs.detect.outputs.entries)[matrix.label].dir
	type lbl struct {
		Label string `json:"label"`
	}
	include := []lbl{}
	nameList := []string{}
	var eb strings.Builder
	eb.WriteString("{")
	for i, en := range es {
		include = append(include, lbl{en.Label})
		nameList = append(nameList, en.Name)
		if i > 0 {
			eb.WriteString(",")
		}
		eb.WriteString(compact(en.Label) + ":" + compact(en))
	}
	eb.WriteString("}")
	outputs := [][2]string{
		{"matrix", compact(map[string]any{"include": include})},
		{"entries", eb.String()},
		{"names", compact(nameList)},
		{"any", strconv.FormatBool(len(es) > 0)},
		{"configured", strconv.Itoa(configured)},
	}
	for _, o := range outputs {
		if err := r.SetOutput(o[0], o[1]); err != nil {
			return fail(r, fmt.Sprintf("cannot write output %s: %v", o[0], err))
		}
	}
	return 0
}

// ValidateReader is `tfpls validate-config`: it reads a config (YAML or JSON) and writes the problems as a JSON array.
func ValidateReader(in io.Reader, out io.Writer) error {
	data, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	cfg, err := yamlconf.Parse(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, compact(Validate(cfg)))
	return err
}
