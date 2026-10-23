package tofu

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const entryJSON = `{"name":"terraform/dev","label":"dev","dir":".","workspace":"dev","var_file":"w/dev.tfvars","parallelism":"7","plan_lock":"false","lock_timeout":"2m","auto_apply":"always","policy_dir":"policies","policy_packs":"safety,aws-tags","policy_params":"{\"safety\":{}}"}`

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// These are the cases of the former tests/entry-input.test.sh.
func TestLoadEntry(t *testing.T) {
	s, err := LoadEntry(env(nil), entryJSON)
	if err != nil || s.Name != "terraform/dev" || s.Label != "dev" || s.Dir != "." || s.Workspace != "dev" {
		t.Fatalf("%+v %v", s, err)
	}
	if s.Parallelism != "7" || s.PlanLock != "false" || s.LockTimeout != "2m" || s.VarFile != "w/dev.tfvars" || s.AutoApply != "always" {
		t.Fatalf("settings: %+v", s)
	}
	if s.PolicyDir != "policies" || s.PolicyPacks != "safety,aws-tags" || s.PolicyParams != `{"safety":{}}` {
		t.Fatalf("policy: %+v", s)
	}
	if s, _ = LoadEntry(env(map[string]string{"AUTO_APPLY": "additive_only"}), entryJSON); s.AutoApply != "additive_only" {
		t.Error("an explicit value must win over the entry")
	}
	if s, err = LoadEntry(env(map[string]string{"NAME": "x", "DIR": "d"}), ""); err != nil || s.Name != "x" || s.Dir != "d" || s.Label != "" {
		t.Errorf("no entry: %+v %v", s, err)
	}
	if s, _ = LoadEntry(env(nil), `{"name":"a"}`); s.Parallelism != "" {
		t.Error("a missing key stays empty")
	}
	// jq's `// "" | tostring`: null and false are empty, numbers and true are their text, a structure is compact JSON
	s, _ = LoadEntry(env(nil), `{"parallelism":5,"plan_lock":false,"var_file":null,"policy_dir":true,"policy_params":{"a": [1, 2]}}`)
	if s.Parallelism != "5" || s.PlanLock != "" || s.VarFile != "" || s.PolicyDir != "true" || s.PolicyParams != `{"a":[1,2]}` {
		t.Errorf("tostring: %+v", s)
	}
}

// The Bash ignored an entry that was not JSON and went on with empty settings (an empty directory is the repository root).
func TestLoadEntryInvalidFailsLoudly(t *testing.T) {
	for _, bad := range []string{"not json", "[1]", "null", `"s"`} {
		if _, err := LoadEntry(env(nil), bad); err == nil {
			t.Errorf("entry %q accepted", bad)
		}
	}
}

// These are the cases of the former tests/target-regex.test.sh.
func TestTargetAllowlist(t *testing.T) {
	for _, ok := range []string{"aws_sqs_queue.extra", "module.vpc", `module.x.aws_s3_bucket.a["k"]`, "aws_instance.web[0]", `module.m.aws_x.y['k']`} {
		if !targetRe.MatchString(ok) {
			t.Errorf("should allow %s", ok)
		}
	}
	for _, bad := range []string{"a;rm", "$(id)", "x`y`", "a b", "a|b", "a&b", "-var=x=1", "", "a>b", `a\\b`, "a\nb", "a b"} {
		if targetRe.MatchString(bad) {
			t.Errorf("should deny %q", bad)
		}
	}
}

func TestFailureLockWithoutHolderClosesTheParenthesis(t *testing.T) {
	// the Bash version printed " (lock `ID`" without the closing parenthesis when the lock had no "Who:" line
	out := Failure("│ Error: Error acquiring the state lock\n│ Lock Info:\n│   ID:        55555555-aaaa-bbbb-cccc-000000000005\n", "dev", "")
	if !strings.Contains(out, "locked (lock `55555555-aaaa-bbbb-cccc-000000000005`). If no run") {
		t.Fatal(out)
	}
}

func TestParseLockReadsTheBoxedOutput(t *testing.T) {
	// plan.txt is tofu's own output: every line carries the box characters. The Bash parser looked for "ID:" at the start of
	// the line and never found it there, so the "state is locked by ... since ..." summary never appeared in a real run.
	text := "╷\n│ Error: Error acquiring the state lock\n│ \n│ Lock Info:\n│   ID:        55555555-aaaa-bbbb-cccc-000000000005\n│   Operation: OperationTypeApply\n│   Who:       crashed@runner\n│   Created:   2026-10-09 10:00:00.123 +0000 UTC\n╵\n"
	l, found, valid := ParseLock(text)
	if !found || !valid || l.Who != "crashed@runner" || l.Op != "OperationTypeApply" || !strings.HasPrefix(l.Created, "2026-10-09 10:00:00") {
		t.Fatalf("%+v %v %v", l, found, valid)
	}
	if _, found, _ := ParseLock("Error: boom"); found {
		t.Error("no lock error, no lock")
	}
}

func TestRollbackAddressWithParenthesis(t *testing.T) {
	// the Bash split the line at the first " (", so an address that contains one was cut in the middle
	out := Rollback("aws_x.a[\"k (1)\"]: Creation complete after 1s\n│ Error: nope\n", "0123456789abcdef")
	if !strings.Contains(out, "- `aws_x.a[\"k (1)\"]` (Creation)") {
		t.Fatal(out)
	}
}

func TestSummarizeOtherActionsAndErrors(t *testing.T) {
	s, err := Summarize([]byte(`{"resource_changes":[{"address":"a","change":{"actions":["delete","create"]}},{"address":"b","change":{"actions":["create","delete"]}},{"address":"c","change":{}}]}`))
	if err != nil || s.Header != "+2 ~0 -2 (2 replace)" || len(s.Resources) != 3 || s.Resources[2].Action != "other" {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := Summarize([]byte("not json")); err == nil {
		t.Error("invalid JSON accepted")
	}
	list, more := Summary{Resources: make([]Resource, 53)}.Changes(50)
	if len(list) != 50 || more != 3 {
		t.Errorf("changes %d more %d", len(list), more)
	}
	if additive([]byte(`{"resource_changes":[{"change":{"actions":["create"]}},{"change":{"actions":["update"]}}]}`)) || additive([]byte("")) || !additive([]byte(`{}`)) {
		t.Error("additive")
	}
}

func TestClipKeepsEndsAndValidUTF8(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "p.txt")
	_ = os.WriteFile(f, []byte(strings.Repeat("ש", 30000)+"\nPlan: 1 to add\n"+strings.Repeat("é", 30000)), 0o644)
	out := ClipPlan(f, 10001)
	if !strings.Contains(out, "bytes omitted") || !strings.Contains(out, "é") || strings.ToValidUTF8(out, "?") != out {
		t.Fatal("clip")
	}
	if ClipPlan(filepath.Join(dir, "none"), 10) != "" {
		t.Error("a missing file is empty")
	}
}
