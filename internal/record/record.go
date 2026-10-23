// Package record appends one audit event (JSON line) to audit/YYYY-MM.jsonl on the data branch.
// Best-effort by design: a failure to write the log never fails a deploy, so Run always returns 0.
package record

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/denzhel/tfpls/internal/datastore"
)

// Env is what Run reads from the outside.
type Env struct {
	Getenv func(string) string
	Stdout io.Writer
	Now    func() time.Time
}

type event struct {
	TS        string          `json:"ts"`
	Repo      string          `json:"repo"`
	Workspace string          `json:"workspace"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	Actor     string          `json:"actor"`
	Trigger   string          `json:"trigger"`
	PR        json.RawMessage `json:"pr"`
	SHA       string          `json:"sha"`
	Run       string          `json:"run"`
	Reviewed  *bool           `json:"reviewed"`
	Counts    json.RawMessage `json:"counts"`
	Summary   string          `json:"summary"`
	LockID    string          `json:"lock_id,omitempty"`
	Via       string          `json:"via,omitempty"`
	Duration  json.RawMessage `json:"duration_s,omitempty"`
}

// number returns the JSON number for s when it is one (what tonumber accepted), else nil.
func number(s string) json.RawMessage {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return json.RawMessage(strconv.FormatInt(n, 10))
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) && !strings.ContainsAny(s, "xXpP_") {
		return json.RawMessage(strconv.FormatFloat(f, 'g', -1, 64))
	}
	return nil
}

// oneLine flattens CR and LF to spaces and keeps at most 300 characters (not bytes: no cut inside a character).
func oneLine(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ").Replace(strings.ToValidUTF8(s, "�"))
	if utf8.RuneCountInString(s) > 300 {
		return string([]rune(s)[:300])
	}
	return s
}

// Build makes the event line; an error says why nothing should be written.
func Build(getenv func(string) string, now string) ([]byte, error) {
	if ws, kind, st := getenv("WORKSPACE"), getenv("KIND"), getenv("STATUS"); ws == "" || kind == "" || st == "" {
		return nil, fmt.Errorf("workspace, kind and status are required")
	}
	counts := json.RawMessage("null")
	if c := strings.TrimSpace(getenv("COUNTS")); strings.HasPrefix(c, "{") {
		var m map[string]any
		var buf bytes.Buffer
		if json.Unmarshal([]byte(c), &m) == nil && json.Compact(&buf, []byte(c)) == nil {
			counts = json.RawMessage(buf.Bytes())
		}
	}
	e := event{TS: now, Repo: getenv("REPO"), Workspace: getenv("WORKSPACE"), Kind: getenv("KIND"), Status: getenv("STATUS"),
		Actor: getenv("ACTOR"), Trigger: getenv("TRIGGER"), PR: json.RawMessage("null"), SHA: getenv("SHA"), Run: getenv("RUN_URL"),
		Counts: counts, Summary: oneLine(getenv("SUMMARY")), LockID: getenv("LOCK_ID"), Via: getenv("VIA")}
	if pr := getenv("PR"); pr != "" {
		if n := number(pr); n != nil {
			e.PR = n
		} else {
			b, _ := json.Marshal(pr)
			e.PR = b
		}
	}
	if r := getenv("REVIEWED"); r != "" {
		v := r == "true"
		e.Reviewed = &v
	}
	if d := getenv("DURATION"); d != "" {
		if n := number(d); n != nil {
			e.Duration = n
		} else {
			e.Duration = json.RawMessage("null")
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// File is the month file for a timestamp (YYYY-MM...).
func File(now string) string {
	if len(now) >= 7 {
		now = now[:7]
	}
	return "audit/" + now + ".jsonl"
}

const readme = "# tfpls data\n\nAudit log (`audit/YYYY-MM.jsonl`, one JSON event per line) written by tfpls. Do not edit by hand.\n"

// Write appends the event line to the month file in the clone dir d and creates the README once.
func Write(d, file string, line []byte) error {
	if err := os.MkdirAll(filepath.Join(d, "audit"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(d, file), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(append([]byte{}, line...), '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	rp := filepath.Join(d, "README.md")
	if _, err := os.Stat(rp); err != nil {
		return os.WriteFile(rp, []byte(readme), 0o644)
	}
	return nil
}

// Run records the event described by the environment. Always 0.
func Run(e Env) int {
	store := datastore.FromEnv(e.Getenv)
	if ok, why := datastore.ValidBranch(store.Branch); !ok {
		fmt.Fprintf(e.Stdout, "::warning::audit log: %s\n", why)
		return 0
	}
	now := e.Getenv("TFPLS_NOW")
	if now == "" {
		now = e.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	line, err := Build(e.Getenv, now)
	if err != nil {
		fmt.Fprintf(e.Stdout, "::warning::audit log: %v\n", err)
		return 0
	}
	file := File(now)
	msg := fmt.Sprintf("audit: %s %s %s", e.Getenv("KIND"), e.Getenv("WORKSPACE"), e.Getenv("STATUS"))
	if err := store.Write(msg, func(d string) error { return Write(d, file, line) }); err != nil {
		fmt.Fprintf(e.Stdout, "::warning::audit log: could not push to %s\n", store.Branch)
		return 0
	}
	fmt.Fprintf(e.Stdout, "audit event recorded in %s:%s\n", store.Branch, file)
	return 0
}
