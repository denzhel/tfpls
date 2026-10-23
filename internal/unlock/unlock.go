package unlock

import (
	"context"
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
	"time"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/ghactions"
	"github.com/denzhel/tfpls/internal/record"
)

// Env is everything the package touches outside the process.
type Env struct {
	Getenv  func(string) string
	Environ func() []string // the environment tofu starts from (the secrets below are removed)
	Stdout  io.Writer
	Stderr  io.Writer
	GH      gh.Client
	Tofu    string // default "tofu"
	Now     func() time.Time
}

// Code of the pull request runs inside tofu (providers, external data sources, modules): it gets neither the GitHub token nor the
// plan key. (The cloud credentials stay: tofu needs them; the trust policy of the role is the control there.)
var withheld = []string{"GH_TOKEN", "GITHUB_TOKEN", "PLAN_ENCRYPTION_KEY"}

var digits = regexp.MustCompile(`^[0-9]+$`)

// Result is what one workspace's check or release ended with.
type Result struct {
	Result string // unlocked | not-locked | locked | refused
	Detail string
	LockID string
	Code   int
}

type params struct {
	name, label, dir, ws, pr string
	reportOnly               bool
}

type run struct {
	e                       Env
	p                       params
	root, runURL, repo, rid string
	extraEnv                []string
	res                     Result
}

func oneLine(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

func (e Env) tofu(ctx context.Context, dir string, extra []string, args ...string) (string, error) {
	bin := e.Tofu
	if bin == "" {
		bin = "tofu"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	var env []string
	for _, kv := range e.Environ() {
		drop := false
		for _, w := range withheld {
			if strings.HasPrefix(kv, w+"=") {
				drop = true
			}
		}
		if !drop {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, extra...)
	b, err := cmd.CombinedOutput()
	return string(b), err
}

func (r *run) reply(text string) {
	if r.p.pr == "" {
		fmt.Fprintln(r.e.Stdout, text)
		return
	}
	r.e.GH.Run("api", "repos/"+r.repo+"/issues/"+r.p.pr+"/comments", "-f", "body="+text)
}

func (r *run) refuse(msg string) Result {
	r.res.Result, r.res.Detail, r.res.Code = "refused", msg, 1
	r.reply("🔒 `" + r.p.label + "` — not unlocked: " + msg + "\n\n[Run logs](" + r.runURL + ")")
	fmt.Fprintln(r.e.Stdout, "refused: "+msg)
	return r.res
}

// cleanup removes tofu's working files from the runner workspace (self-hosted runners keep it). Never acts on empty variables.
func cleanup(root, dir string) {
	if root == "" || dir == "" {
		return
	}
	d := filepath.Join(root, dir)
	if st, err := os.Stat(d); err == nil && st.IsDir() {
		for _, f := range []string{"init.txt", "unlock.txt", ".terraform"} {
			os.RemoveAll(filepath.Join(d, f))
		}
	}
}

func inside(root, dir string) (string, bool) {
	d := filepath.Join(root, dir)
	rel, err := filepath.Rel(root, d)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return d, true
}

func firstChars(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

func (e Env) one(ctx context.Context, p params, extra []string) Result {
	g := e.Getenv
	r := &run{e: e, p: p, root: g("REPO_ROOT"), runURL: g("RUN_URL"), repo: g("GITHUB_REPOSITORY"), rid: g("GITHUB_RUN_ID"), extraEnv: extra}
	defer cleanup(r.root, p.dir)
	if p.pr != "" && !digits.MatchString(p.pr) {
		r.p.pr = ""
		return r.refuse("invalid pull request number")
	}
	dir, ok := inside(r.root, p.dir)
	if st, err := os.Stat(dir); !ok || err != nil || !st.IsDir() {
		return r.refuse("directory `" + p.dir + "` not found")
	}
	if _, err := e.tofu(ctx, dir, extra, "init", "-input=false", "-no-color"); err != nil {
		return r.refuse("init failed")
	}
	if p.ws != "" {
		if strings.HasPrefix(p.ws, "-") {
			return r.refuse("invalid workspace name `" + p.ws + "`")
		}
		if _, err := e.tofu(ctx, dir, extra, "workspace", "select", p.ws); err != nil {
			// a workspace that was never created (new directory, nothing applied yet) has no state and no lock: not a failure when only looking
			if p.reportOnly {
				r.res = Result{Result: "not-locked", Detail: "workspace `" + p.ws + "` does not exist yet"}
				fmt.Fprintln(e.Stdout, "not locked: workspace "+p.ws+" does not exist yet")
				return r.res
			}
			return r.refuse("workspace `" + p.ws + "` does not exist")
		}
	}

	// 1. is it locked? `state mv -dry-run` takes the lock for a moment (fails at once when it is held), needs no variables and no
	//    providers, and moves nothing: the source address does not exist, so it ends with "Invalid source address" or "No state file"
	out, _ := e.tofu(ctx, dir, extra, "state", "mv", "-no-color", "-dry-run", "-lock-timeout=0s", "tfpls_probe.a", "tfpls_probe.b")
	if !strings.Contains(out, lockError) {
		for _, s := range []string{"No state file was found", "Invalid source address", "does not match anything"} {
			if strings.Contains(out, s) {
				r.res = Result{Result: "not-locked", Detail: "not locked"}
				r.reply("🔓 `" + p.label + "` is not locked, nothing to do.\n\n[Run logs](" + r.runURL + ")")
				fmt.Fprintln(e.Stdout, "not locked")
				return r.res
			}
		}
		return r.refuse("could not tell whether it is locked (the probe failed for another reason, see the run logs)")
	}
	l, rc := ParseLock(out)
	if rc != 0 {
		return r.refuse("found a lock but could not read its ID")
	}
	r.res.LockID = l.ID
	age, haveAge := AgeMinutes(l.Created, e.Now())
	// only look: report who holds the lock and for how long, release nothing (the default of the `locks` task)
	if p.reportOnly {
		who, op, a := l.Who, l.Op, "?"
		if who == "" {
			who = "unknown"
		}
		if op == "" {
			op = "unknown"
		}
		if haveAge {
			a = strconv.Itoa(age)
		}
		created := l.Created
		if i := strings.Index(created, "."); i >= 0 {
			created = created[:i]
		}
		r.res.Result = "locked"
		r.res.Detail = fmt.Sprintf("held by `%s` since %s (%s min), operation %s, lock %s", who, created, a, op, l.ID)
		fmt.Fprintf(e.Stdout, "locked: %s since %s (%s)\n", l.Who, l.Created, l.Op)
		return r.res
	}

	// 2. refuse if the lock is fresh (probably a live run)
	if !haveAge {
		return r.refuse("could not read the age of the lock (" + l.Created + "), so it is left alone")
	}
	if age < 5 {
		return r.refuse(fmt.Sprintf("the lock is only %d min old (minimum 5); it is probably still in use. Holder: `%s`, operation: `%s`", age, l.Who, l.Op))
	}

	// 3. refuse if any pipeline job for this workspace is still queued or running
	active := ""
	for _, st := range []string{"in_progress", "queued"} {
		o, err := r.e.GH.Run("api", "--paginate", "repos/"+r.repo+"/actions/runs?status="+st+"&per_page=100", "--jq", ".workflow_runs[].id")
		if err != nil {
			return r.refuse("could not list active runs (does the workflow grant `actions: read`?)")
		}
		var ids []string
		for _, id := range strings.Split(o, "\n") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		// all pages are read; with a very large number of active runs the answer is "cannot tell", never "nothing is running"
		if len(ids) > 300 {
			return r.refuse("more than 300 runs are active, so it cannot be checked whether one of them holds this lock")
		}
		for _, id := range ids {
			if id == r.rid {
				continue
			}
			if !digits.MatchString(id) {
				return r.refuse("could not inspect run " + id)
			}
			var urls []string
			err := r.e.GH.Pages(func(dec *json.Decoder) error {
				var page struct {
					Jobs []struct {
						Name, Status string
						HTMLURL      string `json:"html_url"`
					} `json:"jobs"`
				}
				if err := dec.Decode(&page); err != nil {
					return err
				}
				for _, j := range page.Jobs {
					if j.Status != "completed" && (strings.HasSuffix(j.Name, "/ "+p.label) || strings.HasSuffix(j.Name, " ("+p.label+")")) {
						urls = append(urls, j.HTMLURL)
					}
				}
				return nil
			}, "repos/"+r.repo+"/actions/runs/"+id+"/jobs?per_page=100")
			if err != nil {
				return r.refuse("could not inspect run " + id)
			}
			for _, u := range urls {
				active += u + "\n"
			}
		}
	}
	if active != "" {
		return r.refuse("a run is still active on this workspace, so the lock is real:\n" + active)
	}

	// 4. release exactly that ID
	if o, err := e.tofu(ctx, dir, extra, "force-unlock", "-force", l.ID); err != nil {
		return r.refuse("`force-unlock` failed: " + firstChars(o, 500))
	}
	r.res.Result = "unlocked"
	r.res.Detail = fmt.Sprintf("lock %s held by %s (%d min old) released", l.ID, l.Who, age)
	r.reply("🔓 `" + p.label + "` — lock released.\n\n| | |\n|---|---|\n| Lock ID | `" + l.ID + "` |\n| Holder | `" + l.Who + "` |\n| Operation | `" + l.Op +
		"` |\n| Taken | " + l.Created + " |\n\n[Run logs](" + r.runURL + ")")
	return r.res
}

// Run is the step: a sweep when ENTRIES is set, else the unlock of one workspace. It returns the exit code.
func Run(e Env) int {
	g := e.Getenv
	if e.Now == nil {
		e.Now = time.Now
	}
	if g("ENTRIES") != "" {
		return e.sweep()
	}
	label := g("LABEL")
	if label == "" {
		label = g("NAME")
	}
	res := e.one(context.Background(), params{name: g("NAME"), label: label, dir: g("DIR"), ws: g("WS"), pr: g("PR"), reportOnly: g("REPORT_ONLY") == "true"}, nil)
	r := &ghactions.Runner{Getenv: g, Stdout: e.Stdout}
	r.SetOutput("result", res.Result)
	r.SetOutput("detail", oneLine(res.Detail))
	if res.LockID != "" {
		r.SetOutput("lock_id", res.LockID)
	}
	return res.Code
}

func (e Env) sweep() int {
	g := e.Getenv
	r := &ghactions.Runner{Getenv: g, Stdout: e.Stdout}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal([]byte(g("ENTRIES")), &entries); err != nil || entries == nil {
		r.Error("sweep_entries must be the entries object of detect-workspaces (label -> entry)")
		return 1
	}
	minutes := 10
	if v, err := strconv.Atoi(g("TIMEOUT_MINUTES")); err == nil && v > 0 {
		minutes = v
	}
	release := strings.Fields(g("RELEASE"))
	named := func(label, name string) bool {
		for _, w := range release {
			if w == label || w == name {
				return true
			}
		}
		return false
	}
	// one provider download for every workspace of this task, not one per workspace
	tmp := g("RUNNER_TEMP")
	if tmp == "" {
		tmp = g("TMPDIR")
	}
	if tmp == "" {
		tmp = "/tmp"
	}
	cache := filepath.Join(tmp, "tfpls-plugin-cache")
	os.MkdirAll(cache, 0o755)
	extra := []string{"TF_PLUGIN_CACHE_DIR=" + cache}

	labels := make([]string, 0, len(entries))
	for l := range entries {
		labels = append(labels, l)
	}
	sort.Strings(labels)
	rows, failed, locked, released := "", 0, 0, 0
	for _, label := range labels {
		var en struct {
			Name      string `json:"name"`
			Dir       string `json:"dir"`
			Workspace string `json:"workspace"`
		}
		bad := json.Unmarshal(entries[label], &en) != nil || en.Name == "" || en.Dir == ""
		mode := "report"
		if named(label, en.Name) {
			mode = "release"
		}
		fmt.Fprintf(e.Stdout, "::group::%s (%s)\n", label, mode)
		var res Result
		if bad {
			res = Result{Detail: "the entry has no name or dir", Code: 1}
			fmt.Fprintln(e.Stdout, "invalid entry: "+res.Detail)
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(minutes)*time.Minute)
			res = e.one(ctx, params{name: en.Name, label: label, dir: en.Dir, ws: en.Workspace, pr: g("PR"), reportOnly: mode == "report"}, extra)
			if ctx.Err() != nil && res.Code != 0 {
				res.Detail = fmt.Sprintf("timed out after %d minutes", minutes)
			}
			cancel()
		}
		fmt.Fprintln(e.Stdout, "::endgroup::")
		state := ""
		switch res.Result {
		case "not-locked":
			state = "free"
		case "locked":
			state = "🔒 locked"
			locked++
		case "unlocked":
			state = "🔓 released"
			released++
		default:
			state = "⚠️ could not check"
		}
		detail := oneLine(res.Detail)
		if res.Code != 0 {
			state = "⚠️ could not check or release"
			d := detail
			if d == "" {
				d = "see the log above"
			}
			r.Error(label + ": " + d)
			failed++
		}
		rows += "| `" + label + "` | " + state + " | " + strings.ReplaceAll(detail, "|", `\|`) + " |\n"
		if res.Result == "unlocked" && g("AUDIT") == "true" {
			over := map[string]string{"WORKSPACE": en.Name, "KIND": "unlock", "STATUS": "unlocked", "LOCK_ID": res.LockID, "VIA": "locks-task",
				"SUMMARY": detail, "TOKEN": g("GH_TOKEN"), "TRIGGER": g("GITHUB_EVENT_NAME"), "RUN_URL": g("RUN_URL")}
			record.Run(record.Env{Stdout: e.Stdout, Now: e.Now, Getenv: func(k string) string {
				if v, ok := over[k]; ok {
					return v
				}
				return g(k)
			}})
		}
	}
	md := "### State locks\n\n| Workspace | State | Details |\n|---|---|---|\n" + rows + "\n"
	if locked > 0 {
		md += "To release a lock that nobody is working on: run the manual task `locks` again with `workspaces` set to the workspace name (or comment `/tofu unlock <workspace>` on a pull request). Nothing is released automatically.\n"
	}
	r.Summary(md)
	fmt.Fprintf(e.Stdout, "%d locked, %d released, %d failed\n", locked, released, failed)
	r.SetOutput("locked", strconv.Itoa(locked))
	r.SetOutput("released", strconv.Itoa(released))
	if failed != 0 {
		return 1
	}
	return 0
}
