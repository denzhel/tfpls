package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/denzhel/tfpls/internal/gh"
)

// `tfpls notify-slack` against a local webhook receiver that records the request (path and body). These were the
// checks of tests/notify.test.sh (a Python receiver, the answers read with a JSON tool); the template checks need the jq
// program, which is what a notify_template runs with, and are skipped when it is not installed.

type hook struct {
	srv  *httptest.Server
	path string
	body string
	got  bool
	fail bool
}

func newHook(t *testing.T) *hook {
	h := &hook{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.path, h.body, h.got = r.URL.Path, string(b), true
		if h.fail {
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(h.srv.Close)
	return h
}

// send runs notify with the env (on top of the defaults of the shell test) and returns the log and the exit code.
func (h *hook) send(t *testing.T, kv ...string) (string, int) {
	t.Helper()
	h.got, h.path, h.body, h.fail = false, "", "", false
	env := map[string]string{"HOOK": h.srv.URL + "/hook", "REPO": "o/r", "NAME": "prod", "KIND": "apply", "URL": "http://run", "SUMMARY": "+0 ~1 -0"}
	for _, a := range kv {
		k, v, _ := strings.Cut(a, "=")
		if k == "STUB_FAIL" {
			h.fail = true
			continue
		}
		env[k] = v
	}
	var out bytes.Buffer
	rc := Run(Env{Getenv: func(k string) string { return env[k] }, Stdout: &out, HTTP: h.srv.Client(), GH: gh.Client{}})
	return out.String(), rc
}

func (h *hook) text(t *testing.T) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(h.body), &m); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, h.body)
	}
	s, _ := m["text"].(string)
	return s
}

func join(a ...any) string {
	var p []string
	for _, x := range a {
		p = append(p, fmt.Sprint(x))
	}
	return strings.Join(p, "|")
}

func cnt(s, sub string) int { return strings.Count(s, sub) }

func is(t *testing.T, name string, got, want any) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s: got %q, want %q", name, fmt.Sprint(got), fmt.Sprint(want))
	}
}

var ctx = []string{"AUTHOR=denzhel", "COMMIT_MESSAGE=Allow GHA - ABC-123", "COMMIT_URL=http://commit", "SHA=abc123"}

func with(base []string, more ...string) []string {
	return append(append([]string{}, base...), more...)
}

const changes = `[{"action":"changed","address":"module.a.aws_iam_policy.p[0]"},{"action":"added","address":"module.b.aws_iam_role.r[\"x\"]"}]`

func TestSlackMessages(t *testing.T) {
	h := newHook(t)
	out, _ := h.send(t, with(ctx, "STATUS=applied", "CHANGES="+changes)...)
	is(t, "slack default: a successful apply", h.text(t), "Successful deployment on `prod` has finished\n"+
		"By @denzhel, <http://commit|Commit Message>: `Allow GHA - ABC-123`\n"+
		"<http://run|Review Logs>\n```\nchanged: module.a.aws_iam_policy.p[0]\nadded: module.b.aws_iam_role.r[\"x\"]\n```")
	is(t, "a delivered message is logged", cnt(out, "slack notification sent for prod (applied)"), 1)
	is(t, "the message is written to the log", cnt(out, "Successful deployment on"), 1)
	is(t, "slack: the webhook URL is used", h.path, "/hook")

	h.send(t, with(ctx, "STATUS=error", "MENTION=<!subteam^S1>", "SUMMARY=apply failed")...)
	is(t, "slack default: a failure mentions the group and says why", h.text(t), "<!subteam^S1> A run on `prod` has failed\n"+
		"By @denzhel, <http://commit|Commit Message>: `Allow GHA - ABC-123`\n<http://run|Review Logs>\napply failed")
	h.send(t, with(ctx, "STATUS=applied", "MENTION=<!subteam^S1>")...)
	is(t, "a success does not mention anyone", cnt(h.text(t), "subteam"), 0)
	out, rc := h.send(t, with(ctx, "STATUS=no-changes")...)
	is(t, "an apply without changes posts nothing", fmt.Sprint(h.got)+"|"+fmt.Sprint(rc), "false|0")
	_ = out
	first := func() string { return strings.SplitN(h.text(t), "\n", 2)[0] }
	h.send(t, with(ctx, "STATUS=drift", "MENTION=@here", "KIND=drift (drifted)")...)
	is(t, "slack default: drift mentions and names the workspace", first(), "@here Drift detected on `prod`")
	h.send(t, with(ctx, "STATUS=in-sync", "KIND=drift (recovered)")...)
	is(t, "slack default: drift recovered", first(), "Drift on `prod` is resolved")
	h.send(t, with(ctx, "STATUS=unlocked", "KIND=stale lock cleanup", "SUMMARY=lock 12 released")...)
	is(t, "slack default: a released stale lock", first(), "Released a stale lock on `prod`")
	second := func() string { return strings.Split(h.text(t), "\n")[1] }
	h.send(t, "STATUS=applied", "PR=7", "KIND=apply", "AUTHOR=alice", "COMMIT_MESSAGE=Add queue", "COMMIT_URL=http://pr/7")
	is(t, "an apply by comment names the commenter and the pull request", second(), "By @alice, <http://pr/7|Commit Message>: `Add queue`")
	h.send(t, "STATUS=applied", "AUTHOR=bot", "COMMIT_MESSAGE=a `tick` and\nsecond line")
	is(t, "backticks are neutralised and only the first line of the message is used", second(), "By @bot, <|Commit Message>: `a 'tick' and`")
	h.send(t, "STATUS=applied", "AUTHOR=bot")
	is(t, "no commit message: only the author", second(), "By @bot")
}

func TestOtherFormats(t *testing.T) {
	h := newHook(t)
	h.send(t, "STATUS=error", "FORMAT=teams")
	var m map[string]any
	_ = json.Unmarshal([]byte(h.body), &m)
	sum, _ := m["summary"].(string)
	txt, _ := m["text"].(string)
	is(t, "teams: a MessageCard with text and summary", fmt.Sprint(m["@type"], ",", strings.Contains(txt, "**o/r**"), ",", len(sum) > 0), "MessageCard,true,true")
	h.send(t, "STATUS=applied", "FORMAT=generic")
	m = nil
	_ = json.Unmarshal([]byte(h.body), &m)
	is(t, "generic: plain JSON fields", join(m["repo"], m["workspace"], m["kind"], m["status"], m["ok"], m["url"]), "o/r|prod|apply|applied|true|http://run")
	h.send(t, with(ctx, "STATUS=applied", "FORMAT=generic", "CHANGES="+changes)...)
	var g struct {
		Author  string
		Commit  struct{ Message string }
		Changes []any
	}
	_ = json.Unmarshal([]byte(strings.ToLower(h.body)), &g)
	is(t, "generic: author, commit and changes are included", fmt.Sprint(g.Author, "|", g.Commit.Message, "|", len(g.Changes)), "denzhel|allow gha - abc-123|2")
	h.send(t, "STATUS=error", "FORMAT=generic")
	m = nil
	_ = json.Unmarshal([]byte(h.body), &m)
	is(t, "generic: ok is false for failures", m["ok"], false)
	h.send(t, "HOOK=", "STATUS=error")
	is(t, "no webhook: nothing is sent", h.got, false)
	out, rc := h.send(t, "STATUS=error", "FORMAT=pager")
	is(t, "unknown format: warning, nothing sent, exit 0", fmt.Sprint(cnt(out, "unknown notification format"), rc, h.got), "1 0 false")
	out, rc = h.send(t, "STATUS=error", "STUB_FAIL=1")
	is(t, "a failing webhook never fails the job", fmt.Sprint(rc, cnt(out, "notification failed")), "0 1")
	h.send(t, "STATUS=error", "SUMMARY="+strings.Repeat("x", 800))
	if len(h.text(t)) >= 700 {
		t.Errorf("a long summary is not cut: %d", len(h.text(t)))
	}
}

func needJQ(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("the jq program is not installed: it is what a notify_template runs with")
	}
}

func TestTemplates(t *testing.T) {
	needJQ(t)
	h := newHook(t)
	ws := t.TempDir()
	write := func(rel, s string) {
		p := filepath.Join(ws, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".github/tfpls/slack.jq", `if .status == "applied" then {text: "custom \(.workspace) \(.changes | length) changes by \(.author)"} else empty end`+"\n")
	tpl := []string{"TEMPLATE=.github/tfpls/slack.jq", "WORKSPACE_DIR=" + ws}
	h.send(t, with(with(ctx, tpl...), "STATUS=applied", "CHANGES="+changes)...)
	is(t, "a custom template: the body is exactly its output", h.body, `{"text":"custom prod 2 changes by denzhel"}`)
	_, rc := h.send(t, with(with(ctx, tpl...), "STATUS=error")...)
	is(t, "a template with empty output sends nothing", fmt.Sprint(h.got, rc), "false 0")

	write("env.jq", `{text: ($ENV | keys | length | tostring), e: (env | keys | length)}`+"\n")
	t.Setenv("SECRET_TOKEN", "hunter2") // the program is started with an empty environment whatever this process has
	h.send(t, with(ctx, "STATUS=applied", "TEMPLATE=env.jq", "WORKSPACE_DIR="+ws, "SECRET_TOKEN=hunter2")...)
	var m map[string]any
	_ = json.Unmarshal([]byte(h.body), &m)
	is(t, "a template sees an empty environment", join(m["text"], m["e"]), "0|0")

	write("broken.jq", "{text: (.status |\n")
	out, rc := h.send(t, with(ctx, "STATUS=applied", "TEMPLATE=broken.jq", "WORKSPACE_DIR="+ws)...)
	is(t, "a broken template: warning, the default message is sent, the job stays green",
		join(cnt(out, "::warning::notify_template failed"), rc, strings.SplitN(h.text(t), "\n", 2)[0]), "1|0|Successful deployment on `prod` has finished")
	write("str.jq", `"just a string"`+"\n")
	out, _ = h.send(t, with(ctx, "STATUS=applied", "TEMPLATE=str.jq", "WORKSPACE_DIR="+ws)...)
	is(t, "a template that does not output an object falls back", join(cnt(out, "exactly one JSON object"), strings.SplitN(h.text(t), "\n", 2)[0]), "1|Successful deployment on `prod` has finished")
	out, _ = h.send(t, with(ctx, "STATUS=applied", "TEMPLATE=missing.jq", "WORKSPACE_DIR="+ws)...)
	is(t, "a missing template falls back", cnt(out, "not found"), 1)
	out, _ = h.send(t, with(ctx, "STATUS=applied", "TEMPLATE=../x.jq", "WORKSPACE_DIR="+ws)...)
	is(t, "a template path outside the repository is refused", cnt(out, "not a relative path"), 1)
	write("t.jq", `{text: "blocks"}`+"\n")
	h.send(t, "STATUS=applied", "FORMAT=teams", "TEMPLATE=t.jq", "WORKSPACE_DIR="+ws)
	is(t, "a template replaces the body for any format", h.body, `{"text":"blocks"}`)

	// from the default branch (a gh stub): a pull request cannot change the message about its own apply
	bin := t.TempDir()
	argsFile := filepath.Join(bin, "gh_args")
	stub := "#!/bin/sh\necho \"$*\" > '" + argsFile + "'\necho '{text: \"from default branch\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h.send(t, "STATUS=applied", "TEMPLATE=.github/tfpls/slack.jq", "TEMPLATE_REF=main", "WORKSPACE_DIR="+ws)
	args, _ := os.ReadFile(argsFile)
	is(t, "TEMPLATE_REF: the template comes from that ref, not the checkout",
		h.text(t)+"|"+fmt.Sprint(cnt(string(args), "contents/.github/tfpls/slack.jq?ref=main")), "from default branch|1")
}

var changeLine = regexp.MustCompile(`(?m)^(added|changed|deleted|replaced): `)

// the cap and a property check: at most 50 listed, each once, valid JSON, bounded
func checkList(t *testing.T, h *hook, name string, arr string, n int) {
	t.Helper()
	h.send(t, with(ctx, "STATUS=applied", "CHANGES="+arr)...)
	text := h.text(t)
	var lines []string
	seen := map[string]bool{}
	for _, l := range strings.Split(text, "\n") {
		if changeLine.MatchString(l) {
			lines = append(lines, l)
			seen[l] = true
		}
	}
	want := n
	if want > 50 {
		want = 50
	}
	if len(lines) != want || len(seen) != want {
		t.Errorf("%s: listed %d, unique %d, want %d", name, len(lines), len(seen), want)
	}
	if nl := len(strings.Split(text, "\n")); nl > 56 {
		t.Errorf("%s: %d lines, more than 56", name, nl)
	}
	if n > 50 && cnt(text, fmt.Sprintf("… %d more, see Review Logs", n-50)) != 1 {
		t.Errorf("%s: no '… %d more' line", name, n-50)
	}
}

func changeList(n, salt int, addr func(i int) string) string {
	var items []string
	for i := 0; i < n; i++ {
		b, _ := json.Marshal(map[string]string{"action": []string{"added", "changed", "deleted", "replaced"}[(i+salt)%4], "address": addr(i)})
		items = append(items, string(b))
	}
	return "[" + strings.Join(items, ",") + "]"
}

func TestChangeListCap(t *testing.T) {
	h := newHook(t)
	for _, n := range []int{0, 1, 49, 50, 51, 120} {
		checkList(t, h, fmt.Sprintf("%d changes", n), changeList(n, 0, func(i int) string { return fmt.Sprintf(`mod.r["k%d"].x"q"[%d]`, i, i) }), n)
	}
	// randomized: 25 arrays of 0-120 entries with quotes, brackets and spaces in the addresses
	rng := rand.New(rand.NewSource(164))
	for k := 1; k <= 25; k++ {
		n, salt := rng.Intn(121), rng.Intn(32768)
		checkList(t, h, fmt.Sprintf("random %d (n=%d)", k, n), changeList(n, salt, func(i int) string { return fmt.Sprintf(`m["a%d"].r[%d] "q" ][`, i*salt, i) }), n)
	}
	h.send(t, with(ctx, "STATUS=applied", `CHANGES=[{"action":"added","address":"a"}]`, "CHANGES_MORE=7")...)
	is(t, "changes_more from the plan is reported", cnt(h.text(t), "… 7 more, see Review Logs"), 1)
	h.send(t, with(ctx, "STATUS=applied", "CHANGES=not json")...)
	is(t, "unparsable changes are ignored, the message still goes", strings.SplitN(h.text(t), "\n", 2)[0], "Successful deployment on `prod` has finished")
}
