package codeowners

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/ghactions"
)

const co = `*  @platform
/workspaces/prod-*/  @alice @org/sre
docs/  @writer
/mail/  someone@example.com
`

func noTeams(string) []string { return nil }

func TestLastMatchingRuleWins(t *testing.T) {
	rules := ParseRules(co)
	if got := OwnersOf(rules, "workspaces/prod-a"); strings.Join(got, " ") != "@alice @org/sre" {
		t.Errorf("%v", got)
	}
	if got := OwnersOf(rules, "x/docs"); strings.Join(got, " ") != "@writer" {
		t.Errorf("docs/ matches at any depth: %v", got)
	}
	if got := OwnersOf(rules, "other"); strings.Join(got, " ") != "@platform" {
		t.Errorf("%v", got)
	}
}

func TestLatestReviewPerPersonWinsAndTiesGoToTheLaterEntry(t *testing.T) {
	rv := `[{"user":{"login":"Alice"},"state":"APPROVED","commit_id":"H","submitted_at":"2026-01-01T10:00:00Z"},
	         {"user":{"login":"alice"},"state":"CHANGES_REQUESTED","commit_id":"H","submitted_at":"2026-01-01T10:00:00Z"}]`
	a, err := Approvers([]byte(rv), "H")
	if err != nil || a["alice"] {
		t.Errorf("a change request at the same second, listed later, cancels: %v %v", a, err)
	}
}

func TestReviewWithoutAuthorCountsForNobodyAndBadJSONFails(t *testing.T) {
	a, err := Approvers([]byte(`[{"user":null,"state":"APPROVED","commit_id":"H"}]`), "H")
	if err != nil || len(a) != 0 {
		t.Errorf("%v %v", a, err)
	}
	if _, err := Approvers([]byte(`{`), "H"); err == nil {
		t.Error("invalid reviews must be an error, not 'nobody approved' by accident")
	}
}

func TestTeamMembersAndEmailOwners(t *testing.T) {
	rv := []byte(`[{"user":{"login":"Dave"},"state":"APPROVED","commit_id":"H","submitted_at":"1"}]`)
	teams := func(team string) []string {
		if team == "@org/sre" {
			return []string{"dave"}
		}
		return nil
	}
	if _, ok, _ := Check(co, rv, "H", []string{"workspaces/prod-a"}, teams); !ok {
		t.Error("a team member satisfies the path")
	}
	msg, ok, _ := Check(co, rv, "H", []string{"mail"}, teams)
	if ok || !strings.Contains(msg, "someone@example.com") {
		t.Errorf("an e-mail owner cannot be matched to a review, so it is not satisfied: %q", msg)
	}
}

func TestGHTeamsRefusesOddNames(t *testing.T) {
	called := false
	dir := t.TempDir()
	stub := filepath.Join(dir, "gh")
	_ = os.WriteFile(stub, []byte("#!/bin/sh\necho called >> "+filepath.Join(dir, "log")+"\necho dave\n"), 0o755)
	_ = called
	tm := GHTeams(gh.Client{Bin: stub}, func(string) string { return "" })
	if got := tm("@org/sre"); len(got) != 1 || got[0] != "dave" {
		t.Errorf("%v", got)
	}
	if got := tm("@org/../x"); got != nil {
		t.Errorf("a team slug with .. or odd characters is never sent to the API: %v", got)
	}
	if got := tm("@org/a b"); got != nil {
		t.Error("space in slug")
	}
}

func TestOwnerPaths(t *testing.T) {
	got, err := OwnerPaths(`{"a":{"owner_paths":"w/a/\nx/"},"b":{"owner_paths":"w/a/"},"c":{}}`)
	if err != nil || strings.Join(got, ",") != "w/a/,x/" {
		t.Errorf("%v %v (BUG fixed: a missing owner_paths used to become the path \"null\")", got, err)
	}
	if _, err := OwnerPaths("nope"); err == nil {
		t.Error("invalid ENTRIES")
	}
}

// a gh stub: contents calls answer from $STUB_CONTENTS / fail with $STUB_ERR, everything else is logged
func stubGH(t *testing.T, contents, errText string) (gh.Client, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	body := "#!/bin/sh\necho \"$*\" >> " + log + "\ncase \"$*\" in\n*contents*)\n"
	if errText != "" {
		body += "echo '" + errText + "' >&2; exit 1;;\n"
	} else {
		body += "printf '%s' '" + contents + "'; exit 0;;\n"
	}
	body += "*) exit 0;;\nesac\n"
	stub := filepath.Join(dir, "gh")
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return gh.Client{Bin: stub}, log
}

func enforceEnv(c gh.Client, env map[string]string, reviews string) (Env, *strings.Builder) {
	var out strings.Builder
	get := func(k string) string { return env[k] }
	return Env{Getenv: get, Runner: &ghactions.Runner{Getenv: get, Stdout: &out}, GH: c, Stdout: &out,
		Reviews: func(string, string) ([]byte, error) { return []byte(reviews), nil }}, &out
}

func TestEnforce(t *testing.T) {
	env := map[string]string{"REPO": "o/r", "BASE": "feat/x#1", "PR": "7", "SHA": "H", "COMMENT_ID": "55",
		"ENTRIES": `{"a":{"owner_paths":"workspaces/prod-a/"}}`}
	c, log := stubGH(t, "", "gh: Not Found (HTTP 404)")
	e, out := enforceEnv(c, env, "[]")
	if code := Enforce(e); code != 0 || !strings.Contains(out.String(), "nothing to enforce") {
		t.Errorf("404 enforces nothing: %d %s", code, out)
	}
	c, log = stubGH(t, "", "gh: Bad Gateway (HTTP 502)")
	e, _ = enforceEnv(c, env, "[]")
	if code := Enforce(e); code != 1 {
		t.Errorf("an API error refuses: %d", code)
	}
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), "pr comment 7") || strings.Contains(string(b), "reactions") {
		t.Errorf("explains on the pull request, no reaction: %s", b)
	}
	// found: refused without the owner's approval, ok with it; the ref is URL-escaped
	c, log = stubGH(t, "/workspaces/prod-*/ @alice\n", "")
	e, out = enforceEnv(c, env, "[]")
	if code := Enforce(e); code != 1 || !strings.Contains(out.String(), "::error::CODEOWNERS approval is missing") {
		t.Errorf("%d %s", code, out)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "ref=feat%2Fx%231") || !strings.Contains(string(b), "reactions") {
		t.Errorf("ref must be escaped and the refusal gets a thumbs-down: %s", b)
	}
	c, _ = stubGH(t, "/workspaces/prod-*/ @alice\n", "")
	e, _ = enforceEnv(c, env, `[{"user":{"login":"alice"},"state":"APPROVED","commit_id":"H","submitted_at":"1"}]`)
	if code := Enforce(e); code != 0 {
		t.Errorf("approved: %d", code)
	}
	env["ENTRIES"] = "[]" // BUG (bash): the empty list was a usage error of the checker, now said in words
	e, out = enforceEnv(c, env, "[]")
	if code := Enforce(e); code != 1 || !strings.Contains(out.String(), "no workspace paths") {
		t.Errorf("%d %s", code, out)
	}
}

func TestCommentIDMustBeNumeric(t *testing.T) {
	env := map[string]string{"REPO": "o/r", "BASE": "main", "PR": "7", "SHA": "H", "COMMENT_ID": "1/../../x",
		"ENTRIES": `[{"owner_paths":"w/"}]`}
	c, log := stubGH(t, "w/ @alice\n", "")
	e, _ := enforceEnv(c, env, "[]")
	Enforce(e)
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "reactions") {
		t.Errorf("non-numeric comment id reached the API: %s", b)
	}
}
