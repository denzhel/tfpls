package planstore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmptyPlanAndPlanCommand(t *testing.T) {
	dir, meta := fixture(t)
	_ = os.WriteFile(filepath.Join(dir, "tfplan"), nil, 0o644) // an empty member is still a member (the draft called it invalid)
	env := envWith(map[string]string{"PLAN_ENCRYPTION_KEY": key}, nil)
	p, err := Pack(env, dir, t.TempDir(), meta)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "o")
	var so, se bytes.Buffer
	if rc := Plan([]string{"decrypt", p, out}, env, &so, &se); rc != 0 || !strings.Contains(so.String(), "extracted to "+out) || !strings.Contains(so.String(), `"tree": "abc123def456789"`) {
		t.Fatalf("plan decrypt rc=%d out=%q err=%q", rc, so.String(), se.String())
	}
	so.Reset()
	se.Reset()
	if rc := Plan([]string{"decrypt", p, out}, envWith(nil, nil), &so, &se); rc != 1 || !strings.Contains(se.String(), "PLAN_ENCRYPTION_KEY is not set") {
		t.Fatalf("plan decrypt without key rc=%d err=%q", rc, se.String())
	}
	if rc := Plan([]string{"bogus", p}, env, &so, &se); rc != 1 {
		t.Fatalf("unknown command rc=%d", rc)
	}
}
