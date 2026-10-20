// Package dashboard builds the status dashboard (one self-contained HTML page and a Markdown summary) from a checkout of the tfpls-data
// branch (drift/*.json, audit/*.jsonl).
package dashboard

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed template.html
var template string

const placeholder = "__TFPLS_DATA__"

// Options are the inputs of Build.
type Options struct {
	Repo      string
	ServerURL string // default https://github.com
	Events    int    // at most this many events of the last 24 hours (the caller defaults to 50, see ParseEvents)
	Now       string // ISO timestamp (TFPLS_NOW); "" = the current time
}

type lastApply struct {
	TS     any `json:"ts"`
	Status any `json:"status"`
	Actor  any `json:"actor"`
	Run    any `json:"run"`
}

type workspace struct {
	Name      string     `json:"name"`
	Stack     string     `json:"stack"`
	State     string     `json:"state"`
	Since     any        `json:"since"`
	Checked   any        `json:"checked"`
	Summary   string     `json:"summary"`
	Issue     any        `json:"issue"`
	IssueURL  any        `json:"issue_url"`
	LastApply *lastApply `json:"last_apply"`
}

type event struct {
	raw       json.RawMessage
	ts        string
	kind      string
	status    string
	workspace string
	actor     any
	fields    map[string]any
}

// tostr is jq's string interpolation of a value.
func tostr(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case json.Number:
		return x.String()
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func decode(b []byte) (map[string]any, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, false
	}
	if _, err := dec.Token(); err == nil { // trailing data after the object
		return nil, false
	}
	return m, true
}

func truthy(v any) bool { return v != nil && v != false }

// Build writes <out>/index.html and <out>/summary.md and returns the numbers of workspaces and events.
func Build(dataDir, outDir string, o Options) (int, int, error) {
	if o.ServerURL == "" {
		o.ServerURL = "https://github.com"
	}
	if o.Events < 0 {
		return 0, 0, fmt.Errorf("events must not be negative")
	}
	nowStr := o.Now
	if nowStr == "" {
		nowStr = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	now, err := time.Parse("2006-01-02T15:04:05Z", nowStr)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid time %q", nowStr)
	}
	cut := now.Add(-24 * time.Hour)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, 0, err
	}

	// drift state files (a file that is not a JSON object is skipped)
	type driftFile struct {
		file string
		m    map[string]any
	}
	var drift []driftFile
	files, _ := filepath.Glob(filepath.Join(dataDir, "drift", "*.json"))
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if m, ok := decode(b); ok {
			drift = append(drift, driftFile{strings.TrimSuffix(filepath.Base(f), ".json"), m})
		}
	}
	// audit events (a line that is not a JSON object is skipped)
	var evs []event
	files, _ = filepath.Glob(filepath.Join(dataDir, "audit", "*.jsonl"))
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			m, ok := decode([]byte(line))
			if !ok {
				continue
			}
			var c bytes.Buffer
			if json.Compact(&c, []byte(line)) != nil {
				continue
			}
			evs = append(evs, event{raw: c.Bytes(), ts: str(m, "ts"), kind: str(m, "kind"), status: str(m, "status"), workspace: str(m, "workspace"), actor: m["actor"], fields: m})
		}
	}
	// newest first (a stable sort, then reversed, like sort_by(.ts) | reverse)
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].ts < evs[j].ts })
	for i, j := 0, len(evs)-1; i < j; i, j = i+1, j-1 {
		evs[i], evs[j] = evs[j], evs[i]
	}
	applied := map[string]*lastApply{}
	for _, e := range evs {
		if e.kind == "apply" && e.status == "applied" && e.workspace != "" {
			if _, ok := applied[e.workspace]; !ok {
				applied[e.workspace] = &lastApply{e.fields["ts"], e.fields["status"], e.fields["actor"], e.fields["run"]}
			}
		}
	}

	var ws []workspace
	known := map[string]bool{}
	for _, d := range drift {
		name := d.file
		if n, ok := d.m["name"].(string); ok { // older state files had no name: best guess from the file name
			name = n
		} else if i := strings.Index(d.file, "_"); i >= 0 {
			name = d.file[:i] + "/" + d.file[i+1:]
		}
		state := "unknown"
		if s, ok := d.m["state"].(string); ok && s != "" {
			state = s
		}
		w := workspace{Name: name, Stack: strings.SplitN(name, "/", 2)[0], State: state, Since: d.m["since"], Checked: d.m["checked"],
			Summary: str(d.m, "summary"), LastApply: applied[name]}
		if state == "drift" || state == "error" {
			w.Issue = d.m["issue"]
			if truthy(d.m["issue"]) {
				w.IssueURL = fmt.Sprintf("%s/%s/issues/%s", o.ServerURL, o.Repo, tostr(d.m["issue"]))
			}
		}
		known[name] = true
		ws = append(ws, w)
	}
	var keys []string
	for k := range applied {
		if !known[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		ws = append(ws, workspace{Name: k, Stack: strings.SplitN(k, "/", 2)[0], State: "unknown", LastApply: applied[k]})
	}
	if ws == nil {
		ws = []workspace{}
	}

	// the feed: the last 24 hours before the build time, at most Events
	feed := []json.RawMessage{}
	var feedEv []event
	for _, e := range evs {
		t, err := time.Parse("2006-01-02T15:04:05Z", e.ts)
		if err != nil || t.Before(cut) {
			continue
		}
		feedEv = append(feedEv, e)
		feed = append(feed, e.raw)
	}
	if len(feed) > o.Events {
		feed, feedEv = feed[:o.Events], feedEv[:o.Events]
	}

	// the page: the default encoding escapes "<" (and ">" and "&"), so embedded JSON cannot close the script element
	data, err := json.Marshal(map[string]any{"generated": nowStr, "repo": o.Repo, "workspaces": ws, "events": feed})
	if err != nil {
		return 0, 0, err
	}
	if !strings.Contains(template, placeholder) {
		return 0, 0, fmt.Errorf("template has no data placeholder")
	}
	page := strings.Replace(template, placeholder, string(data)+"\n", 1)
	if err := os.WriteFile(filepath.Join(outDir, "index.html"), []byte(page), 0o644); err != nil {
		return 0, 0, err
	}
	if err := os.WriteFile(filepath.Join(outDir, "summary.md"), []byte(summary(o.Repo, ws, feedEv)), 0o644); err != nil {
		return 0, 0, err
	}
	return len(ws), len(feed), nil
}

func cell(s string) string {
	return strings.NewReplacer("|", "/", "\r", " ", "\n", " ").Replace(s)
}

// summary is the Markdown for the job summary: the same facts, readable without opening the page.
func summary(repo string, ws []workspace, ev []event) string {
	icon := map[string]string{"in-sync": "🟢", "drift": "🟠", "error": "🔴"}
	count := func(s string) int {
		n := 0
		for _, w := range ws {
			if w.State == s {
				n++
			}
		}
		return n
	}
	rank := func(s string) int {
		switch s {
		case "error":
			return 0
		case "drift":
			return 1
		case "unknown":
			return 2
		}
		return 3
	}
	sorted := append([]workspace(nil), ws...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if rank(a.State) != rank(b.State) {
			return rank(a.State) < rank(b.State)
		}
		return a.Name < b.Name
	})
	var b strings.Builder
	fmt.Fprintf(&b, "## Infrastructure status — %s\n\n%d workspaces: %d in sync, %d drifted, %d failing\n\n| Workspace | Status | Pending | Checked |\n|---|---|---|---|\n",
		repo, len(ws), count("in-sync"), count("drift"), count("error"))
	rows := make([]string, len(sorted))
	for i, w := range sorted {
		ic := icon[w.State]
		if ic == "" {
			ic = "⚪"
		}
		pending := ""
		if w.State != "in-sync" {
			pending = cell(w.Summary)
		}
		checked := "–"
		if truthy(w.Checked) {
			checked = cell(tostr(w.Checked))
		}
		rows[i] = fmt.Sprintf("| `%s` | %s %s | %s | %s |", cell(w.Name), ic, cell(w.State), pending, checked)
	}
	b.WriteString(strings.Join(rows, "\n"))
	b.WriteString("\n\n### Recent activity\n\n")
	if len(ev) == 0 {
		b.WriteString("_No audit events in the last 24 hours._")
	} else {
		if len(ev) > 10 {
			ev = ev[:10]
		}
		lines := make([]string, len(ev))
		for i, e := range ev {
			l := fmt.Sprintf("- %s · %s `%s` · %s", e.ts, e.kind, e.workspace, e.status)
			if a, ok := e.actor.(string); ok && a != "" {
				l += " · " + a
			}
			lines[i] = l
		}
		b.WriteString(strings.Join(lines, "\n"))
	}
	b.WriteString("\n")
	return b.String()
}

// ParseEvents reads the EVENTS setting.
func ParseEvents(s string) (int, error) {
	if s == "" {
		return 50, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("events must be a non-negative number, got %q", s)
	}
	return n, nil
}
