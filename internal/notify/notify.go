// Package notify posts a status message to an incoming webhook: Slack (default), Microsoft Teams, or generic JSON.
// It never fails the job. The default Slack message is built here; a repository's own notify_template is a jq file, which is
// the one thing still run with the `jq` binary (an argument list, an empty environment, a time limit): it is a documented feature.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/denzhel/tfpls/internal/gh"
)

// Env is injected for tests.
type Env struct {
	Getenv func(string) string
	Stdout io.Writer
	GH     gh.Client
	HTTP   *http.Client
	JQ     string // path of jq; "" = look it up on the PATH
}

type commit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	URL     string `json:"url"`
}

type change struct {
	Action  string `json:"action"`
	Address string `json:"address"`
}

// run is the run JSON that a template reads (docs/notifications.md).
type run struct {
	Repo        string          `json:"repo"`
	Workspace   string          `json:"workspace"`
	Kind        string          `json:"kind"`
	Status      string          `json:"status"`
	OK          bool            `json:"ok"`
	Summary     string          `json:"summary"`
	Counts      json.RawMessage `json:"counts"`
	Changes     []change        `json:"changes"`
	ChangesMore int             `json:"changes_more"`
	Author      string          `json:"author"`
	Commit      commit          `json:"commit"`
	PR          json.RawMessage `json:"pr"`
	RunURL      string          `json:"run_url"`
	Mention     string          `json:"mention"`
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func flat(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

func tostring(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return "null"
	}
	return b.String()
}

// parseChanges is what the former shell version did: an array's objects with action and address as strings, anything else is nothing.
func parseChanges(raw string) []change {
	var arr []json.RawMessage
	if json.Unmarshal([]byte(raw), &arr) != nil {
		return nil
	}
	var out []change
	for _, el := range arr {
		var m map[string]json.RawMessage
		if json.Unmarshal(el, &m) != nil || m == nil {
			continue
		}
		get := func(k string) string {
			v, ok := m[k]
			if !ok {
				return "null"
			}
			return tostring(v)
		}
		out = append(out, change{Action: get("action"), Address: get("address")})
	}
	return out
}

func number(s string) json.RawMessage {
	if _, err := strconv.ParseFloat(s, 64); err == nil && json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	return nil
}

func build(getenv func(string) string) (r run, emoji, ping, kindText string) {
	status, kind := getenv("STATUS"), getenv("KIND")
	good := false
	switch status {
	case "applied", "no-changes", "unlocked", "in-sync":
		good, emoji = true, "✅"
	case "planned", "skipped":
		good, emoji = true, "📝"
	case "drift", "drifted":
		emoji = "⚠️"
	default:
		emoji = "❌"
	}
	if !good && getenv("MENTION") != "" { // only call people when something is wrong
		ping = getenv("MENTION") + " "
	}
	pr := getenv("PR")
	kindText = kind
	if pr != "" {
		kindText = kind + " (PR #" + pr + ")"
	}
	kindID := kind
	switch {
	case strings.HasPrefix(kind, "apply"):
		kindID = "apply"
	case strings.HasPrefix(kind, "drift"):
		kindID = "drift"
	case strings.Contains(kind, "lock"):
		kindID = "unlock"
	}
	msg := getenv("COMMIT_MESSAGE")
	if i := strings.Index(msg, "\n"); i >= 0 {
		msg = msg[:i]
	}
	changes := parseChanges(getenv("CHANGES"))
	more := 0
	if f, err := strconv.ParseFloat(getenv("CHANGES_MORE"), 64); err == nil && f > 0 {
		more = int(f)
	}
	if len(changes)-50 > more {
		more = len(changes) - 50
	}
	if len(changes) > 50 {
		changes = changes[:50]
	}
	if changes == nil {
		changes = []change{}
	}
	counts := json.RawMessage("null")
	if c := getenv("COUNTS"); c != "" && json.Valid([]byte(c)) {
		counts = json.RawMessage(c)
	}
	prJSON := json.RawMessage("null")
	if pr != "" {
		if n := number(pr); n != nil {
			prJSON = n
		} else {
			prJSON, _ = json.Marshal(pr)
		}
	}
	r = run{Repo: getenv("REPO"), Workspace: getenv("NAME"), Kind: kindID, Status: status, OK: good,
		Summary: clip(flat(getenv("SUMMARY")), 500), Counts: counts, Changes: changes, ChangesMore: more, Author: getenv("AUTHOR"),
		Commit: commit{SHA: getenv("SHA"), Message: clip(msg, 200), URL: getenv("COMMIT_URL")}, PR: prJSON, RunURL: getenv("URL"), Mention: ping}
	return
}

func bt(s string) string { return strings.ReplaceAll(s, "`", "'") }

// defaultSlack is the former default.jq: the Slack text, or "" when nothing should be sent.
func defaultSlack(r run) string {
	if r.Status == "no-changes" && r.Kind != "unlock" {
		return ""
	}
	var head string
	q := "`" + r.Workspace + "`"
	switch {
	case r.Kind == "unlock":
		head = "Released a stale lock on " + q
	case r.Kind == "drift":
		switch r.Status {
		case "in-sync":
			head = "Drift on " + q + " is resolved"
		case "drift", "drifted":
			head = r.Mention + "Drift detected on " + q
		default:
			head = r.Mention + "A drift run on " + q + " has failed"
		}
	case r.Status == "applied":
		head = "Successful deployment on " + q + " has finished"
	case r.Status == "planned":
		head = "Planned " + q + ": nothing was applied"
	case r.Status == "skipped":
		head = "Skipped " + q
	default:
		head = r.Mention + "A run on " + q + " has failed"
	}
	by := "By @" + r.Author
	if r.Commit.Message != "" {
		by = "By @" + r.Author + ", <" + r.Commit.URL + "|Commit Message>: `" + bt(r.Commit.Message) + "`"
	}
	parts := []string{head, by, "<" + r.RunURL + "|Review Logs>"}
	switch r.Status {
	case "applied", "planned", "in-sync":
	default:
		if r.Summary != "" {
			d := bt(r.Summary)
			if rr := []rune(d); len(rr) > 300 {
				d = string(rr[:300]) + "…"
			}
			parts = append(parts, d)
		}
	}
	text := strings.Join(parts, "\n")
	if len(r.Changes) > 0 {
		lines := make([]string, len(r.Changes))
		for i, c := range r.Changes {
			lines[i] = c.Action + ": " + bt(c.Address)
		}
		text += "\n```\n" + strings.Join(lines, "\n")
		if r.ChangesMore > 0 {
			text += fmt.Sprintf("\n… %d more, see Review Logs", r.ChangesMore)
		}
		text += "\n```"
	}
	return text
}

func marshal(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

// render runs a jq file over the run JSON with an empty environment and a time limit.
func (e Env) render(file string, runJSON []byte) (string, string, error) {
	jq := e.JQ
	if jq == "" {
		p, err := exec.LookPath("jq")
		if err != nil {
			return "", "", err
		}
		jq = p
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, jq, "-c", "-f", file)
	cmd.Env = []string{}
	cmd.Stdin = bytes.NewReader(runJSON)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func oneObject(out string) bool {
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if l != "" {
			n++
		}
	}
	if n != 1 {
		return false
	}
	var m map[string]json.RawMessage
	return json.Unmarshal([]byte(strings.TrimSpace(out)), &m) == nil && m != nil
}

// templateFile resolves notify_template to a file, or warns and returns "".
func (e Env) templateFile(dir, tpl, ref, repo string) string {
	w := func(f string, a ...any) { fmt.Fprintf(e.Stdout, f+"\n", a...) }
	if strings.HasPrefix(tpl, "/") || strings.Contains(tpl, "..") {
		w("::warning::notify_template `%s` is not a relative path inside the repository; using the default message", tpl)
		return ""
	}
	if ref != "" {
		// read it from the default branch: a pull request must not change the message about its own apply
		body, err := e.GH.Run("api", "-H", "Accept: application/vnd.github.raw", "repos/"+repo+"/contents/"+tpl+"?ref="+ref)
		if err != nil || body == "" {
			w("::warning::could not read notify_template `%s` from `%s`; using the default message", tpl, ref)
			return ""
		}
		f, err := os.CreateTemp("", "tfpls-template-*.jq")
		if err != nil {
			return ""
		}
		f.WriteString(body)
		f.Close()
		return f.Name()
	}
	if dir == "" {
		dir = "."
	}
	path := filepath.Join(dir, tpl)
	if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
		w("::warning::notify_template `%s` not found; using the default message", tpl)
		return ""
	}
	// a symlink must not lead out of the checkout
	rp, err1 := filepath.EvalSymlinks(path)
	rd, err2 := filepath.EvalSymlinks(dir)
	if err1 != nil || err2 != nil || (rp != rd && !strings.HasPrefix(rp, rd+string(filepath.Separator))) {
		w("::warning::notify_template `%s` is not a relative path inside the repository; using the default message", tpl)
		return ""
	}
	return path
}

// Run implements `tfpls notify-slack`. It always returns 0.
func Run(e Env) int {
	g := e.Getenv
	hook := g("HOOK")
	if hook == "" {
		return 0
	}
	w := func(f string, a ...any) { fmt.Fprintf(e.Stdout, f+"\n", a...) }
	format := g("FORMAT")
	if format == "" {
		format = "slack"
	}
	r, emoji, ping, kindText := build(g)
	runJSON := []byte(marshal(r))
	name, status := g("NAME"), r.Status

	body, have := "", false
	if tpl := g("TEMPLATE"); tpl != "" {
		if file := e.templateFile(g("WORKSPACE_DIR"), tpl, g("TEMPLATE_REF"), g("REPO")); file != "" {
			if g("TEMPLATE_REF") != "" {
				defer os.Remove(file)
			}
			out, stderr, err := e.render(file, runJSON)
			switch {
			case err != nil:
				w("::warning::notify_template failed (%s); using the default message", strings.ReplaceAll(clip(stderr, 200), "\n", " "))
			case strings.TrimSpace(out) == "" || strings.TrimRight(out, "\n") == "null":
				w("notify_template produced no message for %s (%s); nothing sent", name, status)
				return 0
			case oneObject(out):
				body, have = strings.TrimRight(out, "\n"), true
			default:
				w("::warning::notify_template must output exactly one JSON object; using the default message")
			}
		}
	}
	if !have {
		switch format {
		case "slack":
			text := defaultSlack(r)
			if text == "" {
				w("no message for %s (%s); nothing sent", name, status)
				return 0
			}
			body = marshal(map[string]string{"text": text})
		case "teams":
			t := ping + emoji + " **" + r.Repo + "** / `" + name + "` — " + kindText + ": **" + status + "**  " + r.Summary + "  [logs](" + r.RunURL + ")"
			body = marshal(struct {
				Type    string `json:"@type"`
				Context string `json:"@context"`
				Summary string `json:"summary"`
				Color   string `json:"themeColor"`
				Text    string `json:"text"`
			}{"MessageCard", "http://schema.org/extensions", r.Repo + " " + name + " " + kindText + " " + status, "0076D7", t})
		case "generic":
			body = marshal(struct {
				Repo        string   `json:"repo"`
				Workspace   string   `json:"workspace"`
				Kind        string   `json:"kind"`
				Status      string   `json:"status"`
				OK          bool     `json:"ok"`
				Summary     string   `json:"summary"`
				URL         string   `json:"url"`
				Author      string   `json:"author"`
				Commit      commit   `json:"commit"`
				Changes     []change `json:"changes"`
				ChangesMore int      `json:"changes_more"`
			}{r.Repo, r.Workspace, r.Kind, r.Status, r.OK, r.Summary, r.RunURL, r.Author, r.Commit, r.Changes, r.ChangesMore})
		default:
			w("::warning::unknown notification format `%s` (slack, teams or generic)", format)
			return 0
		}
	}
	// the message goes to the log too (addresses and names only, no plan values), so a run can be checked without Slack
	w("::group::message for %s (%s)", name, status)
	fmt.Fprint(e.Stdout, clip(body+"\n", 3000))
	fmt.Fprintln(e.Stdout)
	w("::endgroup::")
	if err := post(e.HTTP, hook, body); err != nil {
		w("::warning::%s notification failed", format) // the error names the URL, a secret: never print it
	} else {
		w("%s notification sent for %s (%s)", format, name, status) // the webhook accepted it (HTTP 2xx)
	}
	return 0
}

func post(c *http.Client, url, body string) error {
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	cl := *c
	cl.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("bad request")
	}
	req.Header.Set("Content-type", "application/json")
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
