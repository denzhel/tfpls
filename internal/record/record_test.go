package record

import (
	"encoding/json"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func build(t *testing.T, m map[string]string) map[string]any {
	t.Helper()
	if m["WORKSPACE"] == "" {
		m["WORKSPACE"] = "dev"
	}
	m["KIND"], m["STATUS"] = "apply", "applied"
	line, err := Build(env(m), "2026-10-10T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(line), "\n") {
		t.Fatalf("line break in %q", line)
	}
	var out map[string]any
	if err := json.Unmarshal(line, &out); err != nil {
		t.Fatalf("not JSON: %v: %s", err, line)
	}
	return out
}

func TestFieldsAndOrder(t *testing.T) {
	line, err := Build(env(map[string]string{"WORKSPACE": "dev", "KIND": "unlock", "STATUS": "unlocked", "LOCK_ID": "x", "VIA": "lock-sweep", "DURATION": "41", "PR": "7", "REVIEWED": "true", "COUNTS": `{"add": 1}`}), "2026-10-10T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ts":"2026-10-10T10:00:00Z","repo":"","workspace":"dev","kind":"unlock","status":"unlocked","actor":"","trigger":"","pr":7,"sha":"","run":"","reviewed":true,"counts":{"add":1},"summary":"","lock_id":"x","via":"lock-sweep","duration_s":41}`
	if string(line) != want {
		t.Errorf("got\n%s\nwant\n%s", line, want)
	}
}

func TestNullsAndOddValues(t *testing.T) {
	e := build(t, map[string]string{})
	if e["pr"] != nil || e["reviewed"] != nil || e["counts"] != nil {
		t.Errorf("want nulls: %v", e)
	}
	if _, ok := e["lock_id"]; ok {
		t.Error("lock_id must be absent")
	}
	if e := build(t, map[string]string{"COUNTS": "not json"}); e["counts"] != nil {
		t.Error("bad counts become null")
	}
	if e := build(t, map[string]string{"COUNTS": "[1]"}); e["counts"] != nil {
		t.Error("an array is not counts")
	}
	if e := build(t, map[string]string{"PR": "abc"}); e["pr"] != "abc" {
		t.Errorf("a non-number PR stays a string: %v", e["pr"])
	}
	if e := build(t, map[string]string{"DURATION": "soon"}); e["duration_s"] != nil {
		t.Errorf("a non-number duration is null: %v", e["duration_s"])
	}
	if e := build(t, map[string]string{"REVIEWED": "false"}); e["reviewed"] != false {
		t.Error("reviewed false")
	}
}

func TestSummaryIsOneLineAndCountedInCharacters(t *testing.T) {
	if e := build(t, map[string]string{"SUMMARY": "line one\r\nline two"}); e["summary"] != "line one  line two" {
		t.Errorf("%q", e["summary"])
	}
	// 200 two-byte characters then more: the old head -c 300 counted bytes and cut inside a character
	long := strings.Repeat("é", 400)
	s := build(t, map[string]string{"SUMMARY": long})["summary"].(string)
	if n := len([]rune(s)); n != 300 || strings.Contains(s, "�") {
		t.Errorf("runes %d, summary has replacement char: %v", n, strings.Contains(s, "�"))
	}
}

func TestRequiredAndFile(t *testing.T) {
	if _, err := Build(env(map[string]string{"WORKSPACE": "dev"}), "x"); err == nil {
		t.Error("kind and status are required")
	}
	if File("2026-10-10T10:00:00Z") != "audit/2026-10.jsonl" {
		t.Error("month file")
	}
}
