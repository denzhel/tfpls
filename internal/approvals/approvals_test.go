package approvals

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeGH struct{ calls [][]string }

func (f *fakeGH) Run(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	return "write", nil
}

func rev(user, state, commit, at string) Review {
	return Review{User: &User{Login: user, Type: "User"}, State: state, CommitID: commit, SubmittedAt: at}
}

func TestCheckModes(t *testing.T) {
	old := []Review{rev("a", "APPROVED", "OLD", "1")}
	if ok, why := Check(old, "HEAD", "strict", 1); ok || !strings.Contains(why, "older commit") {
		t.Errorf("strict old: %v %q", ok, why)
	}
	if ok, _ := Check(old, "HEAD", "basic", 1); !ok {
		t.Error("basic old should pass")
	}
	if ok, _ := Check(nil, "HEAD", "strict", 0); !ok {
		t.Error("zero required")
	}
	later := []Review{rev("a", "APPROVED", "HEAD", "1"), rev("a", "CHANGES_REQUESTED", "HEAD", "2")}
	if ok, _ := Check(later, "HEAD", "basic", 1); ok {
		t.Error("a later change request cancels the approval")
	}
	twice := []Review{rev("a", "APPROVED", "HEAD", "1"), rev("a", "APPROVED", "HEAD", "2")}
	if ok, why := Check(twice, "HEAD", "basic", 2); ok || !strings.Contains(why, "found 1") {
		t.Errorf("same reviewer twice counts once: %q", why)
	}
}

func TestParseRuleFailsClosed(t *testing.T) {
	for _, c := range [][2]string{{"sloppy", "1"}, {"strict", "-1"}, {"strict", "x"}, {"strict", "01"}, {"strict", ""}} {
		if _, err := ParseRule(c[0], c[1]); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
}

func TestGlobAndNormDir(t *testing.T) {
	if normDir("./envs//prod/") != "envs/prod" || normDir("envs/x/../prod") != "envs/prod" || normDir(nil) != "." || normDir("..") != "." {
		t.Error("normDir")
	}
	heads := []J{{"name": "x/prod-eu", "label": "prod-eu", "dir": ".", "workspace": "prod-eu"}}
	base := J{"overrides": J{"prod*": J{"policy_packs": []any{"p3"}}}}
	if r := Compute(heads, base); r.Rules[0]["policy_packs"] != "p3" || r.Entries[0]["policy_packs"] != "p3" {
		t.Errorf("%v", r.Rules)
	}
	// a pull request cannot escape a protected directory by renaming its stack
	prot := J{"stacks": []any{J{"name": "prod", "dir": "envs/prod", "workspaces": []any{"default"}, "policy_packs": []any{"p2"}}}}
	if r := Compute([]J{{"name": "x", "label": "alias", "dir": "envs//prod", "workspace": "default"}}, prot); r.Rules[0]["policy_packs"] != "p2" {
		t.Errorf("alias escaped: %v", r.Rules)
	}
}

func TestSettingsFromRuleset(t *testing.T) {
	var rs []any
	_ = json.Unmarshal([]byte(`[{"type":"pull_request","parameters":{"required_approving_review_count":1}},{"type":"pull_request","parameters":{"required_approving_review_count":3,"dismiss_stale_reviews_on_push":true,"require_code_owner_review":true,"require_last_push_approval":true}}]`), &rs)
	if s := SettingsFromRuleset(rs); s != (Settings{3, "strict", true, true}) {
		t.Errorf("%+v", s)
	}
	if s := SettingsFromRuleset([]any{J{"type": "deletion"}}); s != (Settings{1, "strict", false, false}) {
		t.Errorf("no pull_request rule: %+v", s)
	}
}

func TestEnforceFailsClosedOnBadEntries(t *testing.T) {
	for _, entries := range []string{"", "not json", "[]", "{}"} {
		var out bytes.Buffer
		env := map[string]string{"ENTRIES": entries}
		if rc := Enforce(Env{Getenv: func(k string) string { return env[k] }, Stdout: &out, GH: &fakeGH{}}); rc != 1 {
			t.Errorf("%q: rc %d", entries, rc)
		}
	}
}

func TestReviewsOnlyWriters(t *testing.T) {
	f := filepath.Join(t.TempDir(), "r.json")
	b, _ := json.Marshal([]Review{rev("bob", "APPROVED", "H", "1"), {User: &User{Login: "bot", Type: "Bot"}, State: "APPROVED"}, {State: "APPROVED"}})
	_ = os.WriteFile(f, b, 0o644)
	gh := &fakeGH{}
	env := map[string]string{"TFPLS_REVIEWS_FILE": f}
	got, ignored, err := Reviews(Env{Getenv: func(k string) string { return env[k] }, GH: gh}, "o/r", "1")
	if err != nil || len(got) != 1 || len(ignored) != 0 || len(gh.calls) != 1 {
		t.Errorf("%v %v %v %d", got, ignored, err, len(gh.calls))
	}
}
