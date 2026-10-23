package tofu

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/ghactions"
	"github.com/denzhel/tfpls/internal/planstore"
)

// Env is everything a run takes from outside: the environment (the action's inputs), the streams and the gh client. tofu,
// conftest and git are started from the PATH with argument lists (no shell).
type Env struct {
	Getenv func(string) string
	Stdout io.Writer
	Stderr io.Writer
	GH     gh.Client
}

// OSEnv is the Env of the real process.
func OSEnv() Env { return Env{Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr} }

const (
	statusError = "error"
	maxBody     = 62000 // GitHub rejects comments over 65536 characters
)

// targetRe is the allowlist of a -target value (prevents flag injection from pull request comments).
var targetRe = regexp.MustCompile(`^[\]\[A-Za-z0-9_."'-]+$`)
var packRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
var stalePlanRe = regexp.MustCompile(`(?i)plan is stale|can no longer be applied`)
var runURLOK = regexp.MustCompile(`^https?://[^\s()]+$`)

type runner struct {
	Env
	gha  *ghactions.Runner
	s    Settings
	root string
	// inputs
	targets, apply, autoApply, pr, planMode, headSHA, repo string
	start                                                  time.Time
}

// state is what one pass of the run builds up (a pass restarts with a fresh state after a stale reviewed plan).
type state struct {
	status, summary, bodyExtra, counts, changes, planNote, appliedURL string
	changesMore                                                       int
	reviewedUsed, additive, noHint                                    bool
}

// Run is `tfpls tofu`: plan (and optionally apply) one workspace. The exit code is 1 for the statuses error and policy-failed.
func Run(e Env) int {
	gha := &ghactions.Runner{Getenv: e.Getenv, Stdout: e.Stdout}
	r := &runner{Env: e, gha: gha, start: time.Now()}
	s, err := LoadEntry(e.Getenv, e.Getenv("ENTRY"))
	if err != nil {
		gha.Error("the matrix entry is not valid: " + err.Error())
		r.out("status", statusError)
		r.out("summary", err.Error())
		return 1
	}
	r.s = s
	r.root = e.Getenv("REPO_ROOT")
	r.targets, r.pr, r.headSHA, r.repo = e.Getenv("TARGETS"), e.Getenv("PR"), e.Getenv("HEAD_SHA"), e.Getenv("GITHUB_REPOSITORY")
	r.apply = orDefault(e.Getenv("APPLY"), "none")
	r.autoApply = orDefault(r.s.AutoApply, "never")
	fmt.Fprintf(e.Stdout, "credentials: %s\n", orDefault(e.Getenv("TFPLS_CREDENTIALS"), "unknown"))
	defer r.cleanup()
	return r.pass(orDefault(e.Getenv("PLAN_MODE"), "fresh"), "")
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func (r *runner) label() string { return orDefault(r.s.Label, r.s.Name) }

// oneline flattens CR and LF: step outputs and annotations are ONE line, and text that comes from a saved plan must not add
// outputs or workflow commands.
func oneline(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

func (r *runner) out(name, value string) { _ = r.gha.SetOutput(name, oneline(value)) }

// cleanup removes everything that can hold secret values (the saved plan, its JSON, plan/apply output) and the provider
// directory, however the run ends (self-hosted runners keep their workspace between jobs).
func (r *runner) cleanup() {
	if r.root == "" || r.s.Dir == "" {
		return
	}
	d := filepath.Join(r.root, r.s.Dir)
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		return
	}
	for _, f := range []string{"tfplan", "plan.json", "plan.txt", "show.txt", "apply.txt", "apply_view.txt", "init.txt", "policy.txt", ".terraform"} {
		_ = os.RemoveAll(filepath.Join(d, f))
	}
}

// command builds a process for the pull request's code: it gets neither the GitHub token nor the plan key (the cloud credentials
// stay: tofu needs them; the trust policy of the role is the control there).
func (r *runner) command(dir, name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.Dir = dir
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "GH_TOKEN", "GITHUB_TOKEN", "PLAN_ENCRYPTION_KEY":
			continue
		}
		c.Env = append(c.Env, kv)
	}
	return c
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 127
}

// toFile runs the command with stdout (and stderr when both) written to a file; it returns the exit code.
func (r *runner) toFile(c *exec.Cmd, file string, both bool) int {
	f, err := os.Create(file)
	if err != nil {
		return 126
	}
	defer f.Close()
	c.Stdout = f
	if both {
		c.Stderr = f
	}
	return r.finishCmd(c)
}

func (r *runner) finishCmd(c *exec.Cmd) int {
	err := c.Run()
	if err != nil && exitCode(err) == 127 {
		fmt.Fprintln(r.Stderr, err.Error())
	}
	return exitCode(err)
}

// tee is `cmd 2>&1 | tee file`.
func (r *runner) tee(c *exec.Cmd, file string) int {
	f, err := os.Create(file)
	if err != nil {
		return 126
	}
	defer f.Close()
	w := io.MultiWriter(f, r.Stdout)
	c.Stdout, c.Stderr = w, w
	return r.finishCmd(c)
}

func readFile(p string) string { b, _ := os.ReadFile(p); return string(b) }

func (st *state) extra(s string) {
	if st.bodyExtra != "" {
		st.bodyExtra += "\n"
	}
	st.bodyExtra += s
}

// pass is one execution of the run (the Bash script, once). A stale reviewed plan starts a second pass in plan mode fresh.
func (r *runner) pass(planMode, fallbackNote string) int {
	st := &state{status: statusError, changes: "[]"}
	if fallbackNote != "" {
		st.bodyExtra = "⚠️ " + fallbackNote
		st.planNote = "**" + r.s.Name + "**: " + fallbackNote
	}
	finish := func() int { return r.finish(st) }

	// ---- validate targets
	var targetArgs []string
	for _, t := range strings.FieldsFunc(r.targets, func(c rune) bool { return c == ' ' || c == '\t' || c == '\n' }) {
		if !targetRe.MatchString(t) {
			st.summary = "Invalid -target value: `" + t + "`"
			return finish()
		}
		targetArgs = append(targetArgs, "-target="+t)
	}

	dir := filepath.Join(r.root, r.s.Dir)
	if rel, err := filepath.Rel(r.root, dir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		st.summary = "dir not found: " + r.s.Dir
		return finish()
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		st.summary = "dir not found: " + r.s.Dir
		return finish()
	}
	p := func(n string) string { return filepath.Join(dir, n) }
	tofu := func(args ...string) *exec.Cmd { return r.command(dir, "tofu", args...) }

	if r.toFile(tofu("init", "-input=false", "-no-color"), p("init.txt"), true) != 0 {
		fmt.Fprint(r.Stdout, readFile(p("init.txt")))
		_ = os.WriteFile(p("plan.txt"), []byte(readFile(p("init.txt"))), 0o644)
		st.summary = "init failed"
		if why := strings.TrimRight(Failure(readFile(p("init.txt")), r.label(), r.Getenv("GITHUB_ACTION_REPOSITORY")), "\n"); why != "" {
			st.extra(why)
		}
		return finish()
	}
	if r.s.Workspace != "" {
		c := tofu("workspace", "select", r.s.Workspace)
		if r.finishCmd(c) != 0 {
			st.summary = fmt.Sprintf("workspace `%s` does not exist: create it once with `tofu workspace new %s` in `%s` (tfpls never creates workspaces)", r.s.Workspace, r.s.Workspace, r.s.Dir)
			return finish()
		}
	}

	version := r.tofuVersion(dir)
	tree := ""
	if b, err := r.command(r.root, "git", "-C", r.root, "rev-parse", "HEAD^{tree}").Output(); err == nil {
		tree = strings.TrimRight(string(b), "\n")
	}

	// ---- should this run apply a *reviewed* plan?
	needReviewed := false
	switch r.apply {
	case "force":
		needReviewed = true
	case "auto":
		needReviewed = r.autoApply != "never"
	}
	if planMode == "fresh" {
		needReviewed = false
	}

	// ---- the apply after a merge, when /tofu apply already applied this workspace
	if r.apply == "auto" && planMode != "fresh" && r.pr != "" {
		if u := r.appliedByComment(); u != "" {
			st.appliedURL = u
			needReviewed = false
			r.gha.Notice("the reviewed plan was already applied by /tofu apply (" + u + "); it is not applied again, this run plans once to confirm that nothing is left")
			st.planNote = "**" + r.s.Name + "**: already applied by `/tofu apply` (" + u + "); the reviewed plan was not applied again, this run planned once to see what is left."
			st.extra("ℹ️ Already applied by `/tofu apply` ([run](" + u + ")), so the reviewed plan was not applied again; this run planned once more to see what is left.")
		}
	}

	useReviewed := false
	pname, plannedAt := "", ""
	pe := planstore.Env{Getenv: r.Getenv, Gh: func(args ...string) ([]byte, error) { o, err := r.GH.Run(args...); return []byte(o), err }}
	if needReviewed {
		pname = planstore.PlanName(r.s.Name, r.headSHA, r.targets)
		reason := ""
		if r.headSHA == "" {
			reason = "no pull request is associated with this commit (direct push?)"
		} else {
			reason = r.loadReviewed(pe, pname, tree, version, dir, &plannedAt)
			if reason == "" {
				useReviewed, st.reviewedUsed = true, true
			}
		}
		if !useReviewed {
			hint := planstore.Hint(pe, reason, r.label(), r.targets)
			if planMode == "reviewed_only" {
				r.gha.Error("No usable reviewed plan: " + reason)
				st.summary = "No reviewed plan to apply: " + reason + ". " + hint
				st.planNote = "**" + r.s.Name + "**: refused to apply, no usable reviewed plan (" + reason + ")"
				return finish()
			}
			st.extra("⚠️ No reviewed plan was used (" + reason + "), so this run planned again.")
			st.planNote = "**" + r.s.Name + "**: no reviewed plan used (" + reason + "); re-planned and applied under `auto_apply`."
			r.gha.Warning("No reviewed plan used: " + reason + "; re-planning")
		}
	}

	rc := 0
	if useReviewed {
		if r.toFile(tofu("show", "-no-color", "tfplan"), p("plan.txt"), true) != 0 {
			st.summary = "could not read the reviewed plan"
			return finish()
		}
		fmt.Fprint(r.Stdout, readFile(p("plan.txt")))
		rc = 2
		if plannedAt == "" {
			plannedAt = "earlier"
		}
		r.gha.Notice("Using the reviewed plan " + pname + " (planned " + plannedAt + "); not re-planning")
		st.planNote = "**" + r.s.Name + "**: used the reviewed plan `" + pname + "` (planned " + plannedAt + ")."
	} else {
		par, lt := orDefault(r.s.Parallelism, "10"), orDefault(r.s.LockTimeout, "0s")
		args := []string{"plan", "-input=false", "-no-color", "-detailed-exitcode", "-parallelism=" + par, "-lock-timeout=" + lt, "-out=tfplan"}
		// a plan that will never be applied (PR plans, /tofu plan, drift reports) may skip the lock so that it cannot block an
		// apply; a plan that may be applied keeps the lock (the apply of the saved plan is rejected if the state moved)
		if orDefault(r.s.PlanLock, "true") == "false" && r.apply == "none" {
			args = append(args, "-lock=false")
		}
		if r.s.VarFile != "" {
			args = append(args, "-var-file="+r.s.VarFile)
		}
		args = append(args, targetArgs...)
		rc = r.tee(tofu(args...), p("plan.txt"))
	}

	if rc != 0 && rc != 2 { // 1 is a failed plan; any other code (a killed process, no tofu) is no plan either
		st.summary = "plan failed"
		text := readFile(p("plan.txt"))
		if l, found, valid := ParseLock(text); found && (valid || l.Who != "" || l.Created != "") {
			who := orDefault(l.Who, "someone")
			since := l.Created
			if i := strings.Index(since, "."); i >= 0 {
				since = since[:i]
			}
			op := ""
			if l.Op != "" {
				op = " (" + l.Op + ")"
			}
			st.summary = oneline("plan failed: the state is locked by " + who + " since " + since + op)
		}
		if why := strings.TrimRight(Failure(text, r.label(), r.Getenv("GITHUB_ACTION_REPOSITORY")), "\n"); why != "" {
			st.extra(why)
		}
		return finish()
	}
	if rc == 0 {
		st.status, st.summary = "no-changes", "No changes."
		if st.appliedURL != "" {
			st.summary = "Already applied by `/tofu apply`; nothing changed since."
		}
		return finish()
	}

	// the readable plan for the comment: tofu's own rendering has no "Refreshing state..." lines; if it fails, filter the output
	if r.toFile(tofu("show", "-no-color", "tfplan"), p("show.txt"), false) != 0 {
		_ = os.WriteFile(p("show.txt"), []byte(StripRefresh(readFile(p("plan.txt")))), 0o644)
	}
	r.toFile(tofu("show", "-json", "tfplan"), p("plan.json"), false)
	planDoc, _ := os.ReadFile(p("plan.json"))
	// the plan is summarized once; header, counts and the changed addresses all come from that JSON
	if sum, err := Summarize(planDoc); err == nil {
		st.summary = sum.Header
		st.counts = compact(sum.Counts)
		list, more := sum.Changes(50)
		st.changes, st.changesMore = compact(list), more
	}
	if st.summary == "" {
		st.summary = "(could not summarize the plan, see the full plan below)"
	}
	st.additive = additive(planDoc)

	// ---- policy (re-evaluated even for a reviewed plan: policy files may have changed since the plan)
	if ret := r.policy(st, dir); ret >= 0 {
		return ret
	}

	// ---- save the plan for later review/apply (PR plans and /tofu plan)
	if r.Getenv("SAVE_PLAN") == "true" && !useReviewed {
		if ret := r.savePlan(st, pe, dir, version, tree); ret >= 0 {
			return ret
		}
	}

	// ---- apply decision
	st.status = "planned"
	doApply := false
	switch r.apply {
	case "force":
		doApply = true
	case "auto":
		if r.autoApply == "always" || (r.autoApply == "additive_only" && st.additive) {
			doApply = true
		} else {
			st.status = "skipped"
			st.extra(fmt.Sprintf("Auto-apply not allowed for this change (auto_apply=`%s`, additive=`%t`). It is applied by `/tofu apply %s` on an approved pull request, or from its drift issue once the next drift check reports it.", r.autoApply, st.additive, r.label()))
			r.gha.Notice(fmt.Sprintf("auto-apply not allowed for this change (auto_apply=%s, additive=%t)", r.autoApply, st.additive))
		}
	}
	// require_reviewed_plan never applies a plan made at apply time: what is left after /tofu apply was not reviewed
	if doApply && st.appliedURL != "" && planMode == "reviewed_only" {
		doApply, st.status = false, statusError
		st.summary = "No reviewed plan to apply: `/tofu apply` already applied the reviewed plan, and a new plan shows changes since then. Apply them through a new pull request, or from the drift issue once the next drift check reports them."
		st.planNote = "**" + r.s.Name + "**: refused to apply: `/tofu apply` already applied the reviewed plan, and a new plan shows changes since then."
		r.gha.Error("No usable reviewed plan: /tofu apply already applied it, and a new plan shows changes since then")
	}

	if doApply {
		par, lt := orDefault(r.s.Parallelism, "10"), orDefault(r.s.LockTimeout, "0s")
		if r.tee(tofu("apply", "-input=false", "-no-color", "-parallelism="+par, "-lock-timeout="+lt, "tfplan"), p("apply.txt")) == 0 {
			st.status = "applied"
			_ = os.WriteFile(p("apply_view.txt"), []byte(ApplyView(readFile(p("apply.txt")))), 0o644)
		} else {
			st.status, st.summary = statusError, "apply failed"
			applyText := readFile(p("apply.txt"))
			stale := stalePlanRe.MatchString(applyText)
			if stale {
				if useReviewed && planMode == "reviewed" {
					// merge-time default: a stale reviewed plan counts as "no usable plan" => re-plan under auto_apply
					r.gha.Warning("reviewed plan is stale; re-planning")
					_ = os.Remove(p("tfplan"))
					return r.pass("fresh", "The reviewed plan was stale (state changed after it was made), so this run planned again and applied under `auto_apply`.")
				}
				st.summary = "The state changed after the plan was made, so the reviewed plan can no longer be applied. Run `/tofu plan " + r.label() + "` again and review the new plan."
			}
			_ = os.WriteFile(p("plan.txt"), []byte(applyText), 0o644)
			if !stale {
				st.extra(strings.TrimRight(Rollback(applyText, r.Getenv("GITHUB_SHA")), "\n"))
			}
		}
	}
	return finish()
}

// tofuVersion is the version of the tofu on the PATH.
func (r *runner) tofuVersion(dir string) string {
	if b, err := r.command(dir, "tofu", "version", "-json").Output(); err == nil {
		var v struct {
			V string `json:"terraform_version"`
		}
		if json.Unmarshal(b, &v) == nil && v.V != "" {
			return v.V
		}
	}
	b, _ := r.command(dir, "tofu", "version").Output()
	first, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimLeftFunc(first, func(c rune) bool { return c < '0' || c > '9' })
}

// loadReviewed fetches, opens and checks the saved plan and copies it to dir/tfplan. It returns the reason when there is no
// usable plan ("" when the plan is in place).
func (r *runner) loadReviewed(pe planstore.Env, pname, tree, version, dir string, plannedAt *string) string {
	tmp, err := os.MkdirTemp(r.Getenv("RUNNER_TEMP"), "tfpls-reviewed-")
	if err != nil {
		return oneline(err.Error())
	}
	defer os.RemoveAll(tmp)
	file, err := planstore.Fetch(pe, r.repo, pname, filepath.Join(tmp, "dl"))
	if err == nil {
		err = planstore.Unpack(pe, file, filepath.Join(tmp, "x"))
	}
	if err == nil {
		err = planstore.CheckMeta(filepath.Join(tmp, "x", "meta.json"), tree, r.s.Name, r.s.Dir, version)
	}
	if err != nil {
		return oneline(err.Error())
	}
	b, err := os.ReadFile(filepath.Join(tmp, "x", "tfplan"))
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, "tfplan"), b, 0o600)
	}
	if err != nil {
		return oneline(err.Error())
	}
	var m struct {
		CreatedAt string `json:"created_at"`
	}
	_ = json.Unmarshal([]byte(readFile(filepath.Join(tmp, "x", "meta.json"))), &m)
	*plannedAt = m.CreatedAt
	return ""
}

// policy runs conftest when there is any policy. It returns the exit code when the run ends here, -1 to go on.
func (r *runner) policy(st *state, dir string) int {
	var args []string
	if r.s.PolicyDir != "" {
		args = append(args, "-p", filepath.Join(r.root, r.s.PolicyDir))
	}
	if r.s.PolicyPacks != "" {
		lib := r.Getenv("POLICY_LIBRARY")
		for _, pack := range strings.Split(r.s.PolicyPacks, ",") {
			pack = strings.ReplaceAll(pack, " ", "")
			if pack == "" {
				continue
			}
			if fi, err := os.Stat(filepath.Join(lib, pack)); !packRe.MatchString(pack) || err != nil || !fi.IsDir() {
				var names []string
				ents, _ := os.ReadDir(lib)
				for _, e := range ents {
					names = append(names, e.Name())
				}
				sort.Strings(names)
				avail := "none"
				if len(names) > 0 {
					avail = strings.Join(names, " ") + " "
				}
				st.summary, st.status = "Unknown policy pack `"+pack+"` (available: "+avail+")", statusError
				return r.finish(st)
			}
			args = append(args, "-p", filepath.Join(lib, pack))
		}
	}
	if len(args) == 0 {
		return -1
	}
	if r.s.PolicyParams != "" {
		var v any
		if json.Unmarshal([]byte(r.s.PolicyParams), &v) == nil {
			pdir, err := os.MkdirTemp("", "tfpls-policy")
			if err == nil {
				defer os.RemoveAll(pdir)
				b, _ := json.Marshal(map[string]any{"params": v})
				if os.WriteFile(filepath.Join(pdir, "params.json"), b, 0o600) == nil {
					args = append(args, "--data", pdir)
				}
			}
		} else {
			r.gha.Warning("policy_params is not valid JSON, ignored")
		}
	}
	policyFile := filepath.Join(dir, "policy.txt")
	c := r.command(dir, "conftest", append(append([]string{"test", "plan.json"}, args...), "--no-color")...)
	if r.toFile(c, policyFile, true) == 0 {
		var warns []string
		for _, l := range lines(readFile(policyFile)) {
			if strings.HasPrefix(l, "WARN") {
				warns = append(warns, l)
			}
		}
		if len(warns) > 0 {
			st.extra(fmt.Sprintf("Policy: ✅ passed with %d warning(s)\n```\n%s\n```", len(warns), strings.TrimRight(cutBytes(strings.Join(warns, "\n")+"\n", 4000), "\n")))
		} else {
			st.extra("Policy: ✅ passed")
		}
		return -1
	}
	st.status = "policy-failed"
	fmt.Fprint(r.Stdout, readFile(policyFile))
	st.extra(fmt.Sprintf("Policy: 🛑 failed\n```\n%s\n```", strings.TrimRight(cutBytes(readFile(policyFile), 8000), "\n")))
	return r.finish(st)
}

// cutBytes is `head -c n` that never leaves half a character.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

type planMeta struct {
	Name       string `json:"name"`
	Dir        string `json:"dir"`
	Version    string `json:"tofu_version"`
	Tree       string `json:"tree"`
	HeadSHA    string `json:"head_sha"`
	Targets    string `json:"targets"`
	CreatedAt  string `json:"created_at"`
	PlanRunURL string `json:"plan_run_url"`
}

// savePlan stores the plan for a later reviewed apply. It returns the exit code when the run ends here, -1 to go on.
func (r *runner) savePlan(st *state, pe planstore.Env, dir, version, tree string) int {
	key := r.Getenv("PLAN_ENCRYPTION_KEY")
	switch {
	case r.headSHA == "":
		r.gha.Warning("not saving the plan: no head SHA")
	case key == "" && r.Getenv("REPO_PRIVATE") != "true":
		// a public repository (or one whose visibility is unknown): an unencrypted plan artifact would be readable by anyone
		st.noHint = true
		st.extra("🔒 Plan not saved: this repository is public, so anyone could download a saved plan (it can contain secret values). Run once `openssl rand -base64 32 | gh secret set PLAN_ENCRYPTION_KEY`, then push or comment `/tofu plan " + r.label() + "`. Until then `/tofu apply` has nothing to apply.")
		r.gha.Warning("plan not saved: public repository without PLAN_ENCRYPTION_KEY")
	default:
		meta := planMeta{r.s.Name, r.s.Dir, version, tree, r.headSHA, r.targets, time.Now().UTC().Format("2006-01-02T15:04:05Z"), r.Getenv("RUN_URL")}
		var mb bytes.Buffer
		enc := json.NewEncoder(&mb)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		_ = enc.Encode(meta)
		mf, err := os.CreateTemp("", "tfpls-meta")
		var spath string
		if err == nil {
			defer os.Remove(mf.Name())
			_, err = mf.Write(mb.Bytes())
			mf.Close()
		}
		if err == nil {
			var sdir string
			if sdir, err = os.MkdirTemp(r.Getenv("RUNNER_TEMP"), "tfpls-save-"); err == nil {
				spath, err = planstore.Pack(pe, dir, sdir, mf.Name())
			}
		}
		if err != nil {
			reason := oneline(err.Error())
			st.summary += " (could not save the plan: " + reason + ")"
			r.gha.Error("could not save the plan: " + reason)
			st.status = statusError
			return r.finish(st)
		}
		r.out("plan_path", spath)
		r.out("plan_name", planstore.PlanName(r.s.Name, r.headSHA, r.targets))
		if strings.HasSuffix(spath, ".tgz") && UnencryptedWarns(r.Getenv("ALLOW_UNENCRYPTED_PLANS")) {
			st.bodyExtra += "\n⚠️ `PLAN_ENCRYPTION_KEY` is not set: the saved plan is stored unencrypted and may contain secret values. See https://github.com/" + orDefault(r.Getenv("GITHUB_ACTION_REPOSITORY"), "denzhel/tfpls") + "/blob/main/docs/reviewed-plans.md."
			r.gha.Warning("PLAN_ENCRYPTION_KEY is not set; the saved plan is unencrypted")
		}
	}
	return -1
}

// appliedByComment tells whether /tofu apply already applied this workspace, for the apply after a merge. A saved plan can be
// applied only once, so applying it again can only fail ("saved plan is stale"). The pull request's comments tell: the bot's
// newest comment of this workspace with the status `applied` is newer than its newest one with the status `planned` (a commit
// planned after the apply posts a newer `planned` comment, and that reviewed plan is applied as usual). Only the bot's own
// comments count, and not the ones of this run (a re-run of the merge run applied it, not /tofu apply). It returns the link to the
// run that applied when that is a run of this repository, else the link to the apply comment, or "" for no. Best effort: when
// the comments cannot be read the answer is no.
func (r *runner) appliedByComment() string {
	cs, err := listComments(r.GH, r.repo, r.pr)
	if err != nil {
		return ""
	}
	marker := "<!-- tfpls:" + r.s.Name + " -->"
	self := r.Getenv("RUN_URL")
	base := ""
	if self != "" {
		if i := strings.LastIndex(self, "/"); i >= 0 {
			base = self[:i+1]
		}
	}
	statusRe := regexp.MustCompile(`^<!-- tfpls-status: ([a-z-]+) -->$`)
	applyRe := regexp.MustCompile(`^### .*\]\(([^()\s]+)\) apply logs$`)
	type entry struct {
		id          int64
		url, status string
		run         string
	}
	var es []entry
	for _, c := range cs {
		if !c.isBot() || !strings.HasPrefix(c.Body, marker) {
			continue
		}
		e := entry{id: c.ID, url: c.HTMLURL}
		for _, l := range strings.Split(strings.ReplaceAll(c.Body, "\r", ""), "\n") {
			if e.status == "" {
				if m := statusRe.FindStringSubmatch(l); m != nil {
					e.status = m[1]
				}
			}
			if e.run == "" {
				if m := applyRe.FindStringSubmatch(l); m != nil && base != "" && strings.HasPrefix(m[1], base) && isDigits(m[1][len(base):]) {
					e.run = m[1]
				}
			}
		}
		es = append(es, e)
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].id < es[j].id })
	var a, p *entry
	for i := range es {
		if es[i].status == "applied" && (es[i].run == "" || es[i].run != self) {
			a = &es[i]
		}
		if es[i].status == "planned" {
			p = &es[i]
		}
	}
	if a == nil || (p != nil && a.id < p.id) {
		return ""
	}
	u := a.run
	if u == "" {
		u = a.url
	}
	if !runURLOK.MatchString(u) {
		return ""
	}
	return u
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// comment posts the plan comment of the run (when there is a pull request).
func (r *runner) comment(st *state) {
	if r.pr == "" {
		return
	}
	marker := "<!-- tfpls:" + r.s.Name + " -->"
	runURL := r.Getenv("RUN_URL")
	// after a merge: /tofu apply had applied this workspace and nothing changed since, so one compact line instead of a plan
	if st.status == "no-changes" && st.appliedURL != "" {
		PostComment(r.GH, r.gha, r.repo, r.pr, marker, strings.TrimRight(AlreadyAppliedBody(marker, r.label(), runURL, st.appliedURL), "\n"))
		return
	}
	icon := "❌"
	switch st.status {
	case "applied":
		icon = "✅"
	case "no-changes":
		icon = "🟢"
	case "planned":
		icon = "📝"
	case "skipped":
		icon = "⏭️"
	case "policy-failed":
		icon = "🛑"
	}
	dir := filepath.Join(r.root, r.s.Dir)
	shown := filepath.Join(dir, "plan.txt")
	nonEmpty := func(n string) bool { fi, err := os.Stat(filepath.Join(dir, n)); return err == nil && fi.Size() > 0 }
	switch st.status {
	case "planned", "applied", "policy-failed":
		if nonEmpty("show.txt") {
			shown = filepath.Join(dir, "show.txt")
		}
	}
	if st.status == "applied" && nonEmpty("apply_view.txt") {
		shown = filepath.Join(dir, "apply_view.txt")
	}
	limit := 45000
	var body string
	for {
		body = strings.TrimRight(CommentBody(marker, icon, st.status, r.label(), runURL, shown, st.summary, st.bodyExtra, limit, st.noHint), "\n")
		// GitHub rejects comments over 65536 characters: shrink the plan until the whole comment fits
		if len(body) <= maxBody || limit <= 4000 {
			break
		}
		limit /= 2
	}
	PostComment(r.GH, r.gha, r.repo, r.pr, marker, body) // a new comment per run; the bot's older ones for this workspace are marked superseded
}

// finish writes the step outputs and the comment and returns the exit code.
func (r *runner) finish(st *state) int {
	r.out("status", st.status)
	r.out("summary", st.summary)
	r.out("reviewed", fmt.Sprint(st.reviewedUsed))
	r.out("counts", st.counts)
	r.out("changes", st.changes)
	r.out("changes_more", fmt.Sprint(st.changesMore))
	r.out("duration", fmt.Sprint(int(time.Since(r.start).Seconds())))
	if st.planNote != "" {
		_ = r.gha.Summary(st.planNote + "\n")
	}
	// the readable plan, clipped like a plan comment, for the callers that show it (the drift issue). It is an output, not a
	// file: self-hosted runners keep their workspace, and cleanup() removes everything that can hold plan text.
	switch st.status {
	case "planned", "skipped", "policy-failed":
		show := filepath.Join(r.root, r.s.Dir, "show.txt")
		if fi, err := os.Stat(show); err == nil && fi.Size() > 0 {
			_ = r.gha.SetOutput("plan_text", strings.TrimRight(ClipPlan(show, 45000), "\n"))
		}
	}
	r.comment(st)
	if st.status == statusError || st.status == "policy-failed" {
		return 1
	}
	return 0
}
