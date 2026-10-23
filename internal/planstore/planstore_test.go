package planstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const key = "correct horse battery staple"

func envWith(vars map[string]string, gh func(args ...string) ([]byte, error)) Env {
	return Env{Getenv: func(k string) string { return vars[k] }, Gh: gh}
}

func fixture(t *testing.T) (dir, meta string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tfplan"), bytes.Repeat([]byte("plan\x00\xff"), 700), 0o644); err != nil {
		t.Fatal(err)
	}
	meta = filepath.Join(dir, "meta.src")
	_ = os.WriteFile(meta, []byte(`{"name":"n/w","dir":".","tofu_version":"1.10.6","tree":"abc123def456789"}`), 0o644)
	return dir, meta
}

func wantMsg(t *testing.T, err error, code int, msg string) {
	t.Helper()
	var e *Err
	if !errors.As(err, &e) || e.Code != code || !strings.Contains(e.Msg, msg) {
		t.Fatalf("got %v, want code %d containing %q", err, code, msg)
	}
}

func TestNames(t *testing.T) {
	if got := Slug("terraform/prod-us-west-1"); got != "terraform__prod-us-west-1" {
		t.Error(got)
	}
	if got := Slug("a b;c"); got != "a_b_c" {
		t.Error(got)
	}
	if TargetsHash("") != "all" || TargetsHash("b.x a.y") != TargetsHash("a.y  b.x") || TargetsHash("a.y") == TargetsHash("a.z") {
		t.Error("targets hash")
	}
	if got := PlanName("x/y", "0123456789abcdef", ""); got != "tfpls-plan-x__y-0123456789ab-all" {
		t.Error(got)
	}
	// the Bash version expands a glob here (it depends on the files of the current directory); Go hashes the literal
	if TargetsHash("*") != "684888c0" {
		t.Error(TargetsHash("*"))
	}
}

func TestRoundTrip(t *testing.T) {
	dir, meta := fixture(t)
	for _, k := range []string{key, ""} {
		env := envWith(map[string]string{"PLAN_ENCRYPTION_KEY": k}, nil)
		p, err := Pack(env, dir, t.TempDir(), meta)
		if err != nil || strings.HasSuffix(p, ".enc") != (k != "") {
			t.Fatalf("pack: %v %s", err, p)
		}
		out := t.TempDir()
		if err := Unpack(env, p, out); err != nil {
			t.Fatal(err)
		}
		a, _ := os.ReadFile(filepath.Join(dir, "tfplan"))
		b, _ := os.ReadFile(filepath.Join(out, "tfplan"))
		if !bytes.Equal(a, b) {
			t.Error("plan bytes differ")
		}
	}
}

func TestRefusals(t *testing.T) {
	dir, meta := fixture(t)
	with := envWith(map[string]string{"PLAN_ENCRYPTION_KEY": key}, nil)
	without := envWith(nil, nil)
	enc, _ := Pack(with, dir, t.TempDir(), meta)
	plain, _ := Pack(without, dir, t.TempDir(), meta)
	o := t.TempDir()

	wantMsg(t, Unpack(envWith(map[string]string{"PLAN_ENCRYPTION_KEY": "wrong"}, nil), enc, o), 1, "could not decrypt the saved plan (wrong key or corrupted file)")
	wantMsg(t, Unpack(without, enc, o), 1, "encrypted but PLAN_ENCRYPTION_KEY is not set")
	wantMsg(t, Unpack(with, plain, o), 1, "refusing a possible downgrade")
	wantMsg(t, Unpack(with, "x.bin", o), 1, "unknown plan file type: x.bin")

	data, _ := os.ReadFile(enc)
	tampered := append([]byte{}, data...)
	tampered[200] ^= 0xff
	f := filepath.Join(t.TempDir(), "t.enc")
	_ = os.WriteFile(f, tampered, 0o600)
	wantMsg(t, Unpack(with, f, o), 1, "integrity check")
	_ = os.WriteFile(f, data[:100], 0o600)
	wantMsg(t, Unpack(with, f, o), 1, "could not decrypt")
	_ = os.WriteFile(f, data[:5], 0o600)
	wantMsg(t, Unpack(with, f, o), 1, "could not decrypt")

	bad := filepath.Join(t.TempDir(), "x.tgz")
	_ = os.WriteFile(bad, []byte("not gzip"), 0o600)
	wantMsg(t, Unpack(without, bad, o), 1, "the saved plan archive is invalid")
	_, err := Pack(with, t.TempDir(), t.TempDir(), meta)
	wantMsg(t, err, 1, "no tfplan in ")
}

func TestCheckMeta(t *testing.T) {
	_, meta := fixture(t)
	if err := CheckMeta(meta, "abc123def456789", "n/w", ".", "1.10.6"); err != nil {
		t.Fatal(err)
	}
	wantMsg(t, CheckMeta(meta, "000000000000", "n/w", ".", "1.10.6"), 1, "different code (plan tree abc123def456, current tree 000000000000); run /tofu plan again")
	wantMsg(t, CheckMeta(meta, "abc123def456789", "dev", ".", "1.10.6"), 1, "workspace 'n/w', not 'dev'")
	wantMsg(t, CheckMeta(meta, "abc123def456789", "n/w", "o", "1.10.6"), 1, "directory '.', not 'o'")
	wantMsg(t, CheckMeta(meta, "abc123def456789", "n/w", ".", "1.11"), 1, "made with tofu 1.10.6, this run uses 1.11")
}

const (
	live     = `{"artifacts":[{"id":2,"expired":false,"created_at":"2026-02-01T00:00:00Z","workflow_run":{"id":22,"repository_id":100,"head_repository_id":100,"head_branch":"main"}}]}`
	expired  = `{"artifacts":[{"id":1,"expired":true,"created_at":"2026-01-01T00:00:00Z","expires_at":"2026-01-15T00:00:00Z","workflow_run":{"id":11,"repository_id":100,"head_repository_id":100,"head_branch":"main"}}]}`
	foreign  = `{"artifacts":[{"id":9,"expired":false,"created_at":"2026-03-01T00:00:00Z","workflow_run":{"id":99,"repository_id":100,"head_repository_id":200,"head_branch":"main"}}]}`
	branches = `{"artifacts":[{"id":9,"expired":false,"created_at":"2026-03-01T00:00:00Z","workflow_run":{"id":99,"repository_id":100,"head_repository_id":100,"head_branch":"mallory","head_sha":"evil"}},{"id":2,"expired":false,"created_at":"2026-02-01T00:00:00Z","workflow_run":{"id":22,"repository_id":100,"head_repository_id":100,"head_branch":"pr-1","head_sha":"abc"}}]}`
)

func TestLookup(t *testing.T) {
	vars := map[string]string{"DEFAULT_BRANCH": "main", "GITHUB_REPOSITORY_ID": "100", "PLAN_ENCRYPTION_KEY": key}
	var calls [][]string
	list := func(body string) Env {
		return envWith(vars, func(a ...string) ([]byte, error) { calls = append(calls, a); return []byte(body), nil })
	}
	if got, err := Lookup(list(live), "o/r", "p"); err != nil || got != "2 22" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := Lookup(list(live+"\n"+foreign), "o/r", "p"); err != nil || got != "2 22" {
		t.Fatalf("paginated stream: %q %v", got, err)
	}
	_, err := Lookup(list(expired), "o/r", "p")
	wantMsg(t, err, 1, "the reviewed plan expired on 2026-01-15 (p)")
	_, err = Lookup(list(`{"artifacts":[]}`), "o/r", "p")
	wantMsg(t, err, 1, "no reviewed plan found (p)")
	_, err = Lookup(list(foreign), "o/r", "p")
	wantMsg(t, err, 1, "no reviewed plan found")
	_, err = Lookup(list("not json"), "o/r", "p")
	wantMsg(t, err, 2, "could not list artifacts")
	vars["HEAD_SHA"] = "abc"
	if got, err := Lookup(list(branches), "o/r", "p"); err != nil || got != "2 22" {
		t.Fatalf("branch plant won: %q %v", got, err)
	}
	vars["HEAD_SHA"] = "zzz"
	_, err = Lookup(list(branches), "o/r", "p")
	wantMsg(t, err, 1, "no reviewed plan found")
	delete(vars, "GITHUB_REPOSITORY_ID")
	_, err = Lookup(list(live), "o/r", "p")
	wantMsg(t, err, 2, "could not tell the repository id")
	fail := envWith(map[string]string{"GITHUB_REPOSITORY_ID": "100"}, func(...string) ([]byte, error) { return nil, errors.New("x") })
	_, err = Lookup(fail, "o/r", "p")
	wantMsg(t, err, 2, "could not tell the default branch")

	found := false
	for _, c := range calls {
		if strings.Contains(strings.Join(c, " "), "--paginate") && strings.Contains(strings.Join(c, " "), "per_page=100") {
			found = true
		}
		if strings.Contains(strings.Join(c, " "), key) {
			t.Error("the key is on a gh command line")
		}
	}
	if !found {
		t.Error("the listing is not paginated with 100 per page")
	}
}

func TestFetchErrors(t *testing.T) {
	vars := map[string]string{"DEFAULT_BRANCH": "main", "GITHUB_REPOSITORY_ID": "100"}
	down := func(a ...string) ([]byte, error) {
		if a[0] == "run" {
			return nil, errors.New("x")
		}
		return []byte(live), nil
	}
	_, err := Fetch(envWith(vars, down), "o/r", "p", t.TempDir())
	wantMsg(t, err, 2, "could not download the reviewed plan artifact (p)")
	empty := func(a ...string) ([]byte, error) { return []byte(live), nil }
	_, err = Fetch(envWith(vars, empty), "o/r", "p", t.TempDir())
	wantMsg(t, err, 2, "the artifact (p) contains no plan file")
}

func TestHint(t *testing.T) {
	e := envWith(nil, nil)
	for reason, want := range map[string]string{
		"the saved plan is encrypted but PLAN_ENCRYPTION_KEY is not set":      "secrets: inherit",
		"could not decrypt the saved plan (wrong key or corrupted file)":      "same PLAN_ENCRYPTION_KEY",
		"the saved plan is NOT encrypted but PLAN_ENCRYPTION_KEY is set; x":   "saved encrypted",
		"could not list artifacts (does the workflow grant 'actions: read'?)": "actions: read",
		"the reviewed plan expired on 2026-01-15 (x)":                         "14 days",
		"the reviewed plan was made for different code (a, b)":                "Update the branch",
		"the reviewed plan was made with tofu 1.9.0, this run uses 1.10.6":    "version changed",
		"no reviewed plan found (x)":                                          "/tofu plan l -target=a -target=b",
	} {
		if got := Hint(e, reason, "l", "a b"); !strings.Contains(got, want) {
			t.Errorf("%s: %s", reason, got)
		}
	}
}

// The key only ever comes from the environment: the CLI has no key argument, and the sources never name openssl's -pass.
func TestKeyNeverInArgv(t *testing.T) {
	src, _ := os.ReadFile("planstore.go")
	cli, _ := os.ReadFile("cli.go")
	if strings.Contains(string(src)+string(cli), "pass:") || strings.Contains(string(cli), "os.Args") {
		t.Error("a key argument")
	}
	var out, errb bytes.Buffer
	if rc := Run([]string{"pack", "a", "b"}, envWith(nil, nil), &out, &errb); rc != 2 {
		t.Errorf("bad arity: rc %d", rc)
	}
}
