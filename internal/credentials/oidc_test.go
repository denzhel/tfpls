package credentials

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/denzhel/tfpls/internal/gh"
)

// The OIDC path of `tfpls env` against fake GitHub-OIDC and STS servers (no cloud, no network). These were the checks of
// tests/oidc.test.sh (a Python server, the logged requests read with a JSON tool).

type fakeCloud struct {
	srv *httptest.Server
	mu  sync.Mutex
	log []string // "GET <path+query> auth=<header>" and "POST <path> <JSON of the form>"
}

func newFakeCloud(t *testing.T) *fakeCloud {
	f := &fakeCloud{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			auth := r.Header.Get("Authorization")
			if auth == "" {
				auth = "None"
			}
			f.log = append(f.log, fmt.Sprintf("GET %s auth=%s", r.URL.RequestURI(), auth))
			switch r.URL.Path {
			case "/jwt":
				pl := base64.RawURLEncoding.EncodeToString([]byte(`{"sub": "repo:acme@1/infra@2:pull_request"}`))
				fmt.Fprintf(w, `{"value": "h.%s.s"}`, pl)
			case "/token":
				fmt.Fprintf(w, `{"value": "jwt-for-%s"}`, r.URL.Query().Get("audience"))
			default:
				w.WriteHeader(404)
			}
		case http.MethodPost:
			_ = r.ParseForm()
			form := map[string]string{}
			for k, v := range r.PostForm {
				form[k] = v[0]
			}
			b, _ := json.Marshal(form)
			f.log = append(f.log, fmt.Sprintf("POST %s %s", r.URL.Path, b))
			if strings.HasSuffix(form["RoleArn"], "/denied") {
				w.WriteHeader(403)
				fmt.Fprint(w, "<ErrorResponse><Error><Code>AccessDenied</Code><Message>Not authorized to perform sts:AssumeRoleWithWebIdentity</Message></Error></ErrorResponse>")
				return
			}
			fmt.Fprint(w, "<AssumeRoleWithWebIdentityResponse><AssumeRoleWithWebIdentityResult><Credentials><AccessKeyId>ASIAAPPLY</AccessKeyId><SecretAccessKey>secret-apply</SecretAccessKey><SessionToken>token-xyz</SessionToken></Credentials></AssumeRoleWithWebIdentityResult></AssumeRoleWithWebIdentityResponse>")
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCloud) lines(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, l := range f.log {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

func (f *fakeCloud) reset() { f.mu.Lock(); f.log = nil; f.mu.Unlock() }

type oidcRun struct {
	rc  int
	out string
	env string // the GITHUB_ENV file
	gho string // the GITHUB_OUTPUT file
}

func (r oidcRun) envvals(key string) []string {
	var vals []string
	lines := strings.Split(r.env, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, key+"<<") && i+1 < len(lines) {
			vals = append(vals, lines[i+1])
		}
	}
	return vals
}

func (r oidcRun) first(key string) string {
	if v := r.envvals(key); len(v) > 0 {
		return v[0]
	}
	return ""
}

func (r oidcRun) last(key string) string {
	if v := r.envvals(key); len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}

func run(t *testing.T, f *fakeCloud, kv ...string) oidcRun {
	t.Helper()
	dir := t.TempDir()
	envFile, ghoFile := filepath.Join(dir, "env"), filepath.Join(dir, "gho")
	for _, p := range []string{envFile, ghoFile} {
		_ = os.WriteFile(p, nil, 0o644)
	}
	env := map[string]string{
		"GITHUB_ENV": envFile, "GITHUB_OUTPUT": ghoFile, "GITHUB_STEP_SUMMARY": os.DevNull,
		"ACTIONS_ID_TOKEN_REQUEST_URL": f.srv.URL + "/token?api=1", "ACTIONS_ID_TOKEN_REQUEST_TOKEN": "req-token", "GITHUB_RUN_ID": "77",
	}
	for _, a := range kv {
		k, v, _ := strings.Cut(a, "=")
		env[k] = v
	}
	var out bytes.Buffer
	rc := Run(Env{Getenv: func(k string) string { return env[k] }, Stdout: &out, GH: gh.Client{}})
	e, _ := os.ReadFile(envFile)
	g, _ := os.ReadFile(ghoFile)
	return oidcRun{rc: rc, out: out.String(), env: string(e), gho: string(g)}
}

func is(t *testing.T, name string, got, want any) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s: got %q, want %q", name, fmt.Sprint(got), fmt.Sprint(want))
	}
}

func count(s, sub string) int { return strings.Count(s, sub) }

const role = "arn:aws:iam::123456789012:role/tfpls-role"

func TestOIDCRole(t *testing.T) {
	f := newFakeCloud(t)
	jsonMap := fmt.Sprintf(`{"AWS_DEFAULT_REGION":"us-east-1","AWS_ENDPOINT_URL_STS":"%s/"}`, f.srv.URL)

	r := run(t, f, "JSON="+jsonMap, "ROLE_ARN="+role)
	is(t, "with a role: short-lived keys from STS", fmt.Sprint(r.rc, "|", r.first("AWS_ACCESS_KEY_ID"), "|", r.first("AWS_SECRET_ACCESS_KEY"), "|", r.first("AWS_SESSION_TOKEN")), "0|ASIAAPPLY|secret-apply|token-xyz")
	is(t, "labelled oidc", r.first("TFPLS_CREDENTIALS")+"|"+strings.TrimSpace(r.gho), "oidc|credentials=oidc")
	is(t, "the keys are masked in the log", fmt.Sprint(count(r.out, "::add-mask::secret-apply"), "|", count(r.out, "::add-mask::jwt-for-")), "1|1")
	is(t, "the OIDC token was requested with the AWS audience and the runner's bearer token",
		len(f.lines("GET /token?api=1&audience=sts.amazonaws.com auth=bearer req-token")), 1)
	posts := f.lines("POST ")
	if len(posts) != 1 {
		t.Fatalf("STS requests: %v", posts)
	}
	var form map[string]string
	_ = json.Unmarshal([]byte(strings.SplitN(posts[0], " ", 3)[2]), &form)
	is(t, "STS got the role, the web identity token and a session name with the run id",
		strings.Join([]string{form["RoleArn"], form["WebIdentityToken"], form["RoleSessionName"], form["Action"]}, "|"), role+"|jwt-for-sts.amazonaws.com|tfpls-77|AssumeRoleWithWebIdentity")

	f.reset()
	r = run(t, f, "ROLE_ARN="+role, `JSON={"AWS_ACCESS_KEY_ID":"static-key-12345"}`, "STS_ENDPOINT="+f.srv.URL+"/")
	is(t, "the role's keys are written after the JSON map, so they win (the last value of an env file counts)", fmt.Sprint(r.rc, "|", r.last("AWS_ACCESS_KEY_ID")), "0|ASIAAPPLY")
	f.reset()
	run(t, f, "JSON="+jsonMap, "ROLE_ARN="+role, "OIDC_AUDIENCE=my-audience")
	is(t, "a custom audience is passed on", len(f.lines("GET /token?api=1&audience=my-audience")), 1)
}

func TestOIDCFailures(t *testing.T) {
	f := newFakeCloud(t)
	jsonMap := fmt.Sprintf(`{"AWS_DEFAULT_REGION":"us-east-1","AWS_ENDPOINT_URL_STS":"%s/"}`, f.srv.URL)

	r := run(t, f, "JSON="+jsonMap, "ROLE_ARN=arn:aws:iam::123456789012:role/denied")
	is(t, "STS refusing the role fails with the reason and a trust-policy hint",
		fmt.Sprint(r.rc, "|", count(r.out, "AccessDenied: Not authorized"), "|", count(r.out, "trust policy"), "|", r.first("AWS_ACCESS_KEY_ID")), "1|1|1|")
	r = run(t, f, "JSON="+jsonMap, "ROLE_ARN="+role, "ACTIONS_ID_TOKEN_REQUEST_URL=")
	is(t, "without id-token: write the error says so", fmt.Sprint(r.rc, "|", count(r.out, "id-token: write")), "1|1")
	f.reset()
	r = run(t, f, "JSON="+jsonMap, "ROLE_ARN=not-an-arn")
	is(t, "a malformed role is refused before anything is requested", fmt.Sprint(r.rc, "|", count(r.out, "not an IAM role ARN"), "|", len(f.lines(""))), "1|1|0")
	r = run(t, f, "JSON="+jsonMap, "ROLE_ARN="+role, "STS_ENDPOINT=http://127.0.0.1:1/")
	is(t, "an unreachable STS fails clearly", fmt.Sprint(r.rc, "|", count(r.out, "could not reach STS")), "1|1")

	// no role: the old behaviour is untouched
	r = run(t, f, `JSON={"AWS_ACCESS_KEY_ID":"static-key-12345"}`)
	is(t, "no role: the static keys of the JSON map", fmt.Sprint(r.rc, "|", r.first("AWS_ACCESS_KEY_ID"), "|", r.first("TFPLS_CREDENTIALS")), "0|static-key-12345|env")
}

// a refused role is explained on the pull request: once, with the token subject, never the role ARN or AWS's message
func TestOIDCRefusedRoleIsExplainedOnThePullRequest(t *testing.T) {
	f := newFakeCloud(t)
	jsonMap := fmt.Sprintf(`{"AWS_DEFAULT_REGION":"us-east-1","AWS_ENDPOINT_URL_STS":"%s/"}`, f.srv.URL)
	bin, state := t.TempDir(), t.TempDir()
	stub := `#!/bin/sh
echo "$*" >> "$STUB_LOG"
case "$*" in
  *"-X PATCH"*|*"issues/"*"/comments -f"*|*"/comments -f body"*) for a in "$@"; do case "$a" in body=*) printf '%s\n' "${a#body=}" >> "$STUB_BODY";; esac; done; echo '{}';;
  *) echo '[]';;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STUB_LOG", filepath.Join(state, "log"))
	t.Setenv("STUB_BODY", filepath.Join(state, "body"))
	jwtURL := f.srv.URL + "/jwt?api=1"
	denied := "arn:aws:iam::123456789012:role/denied"

	r := run(t, f, "JSON="+jsonMap, "ROLE_ARN="+denied, "ACTIONS_ID_TOKEN_REQUEST_URL="+jwtURL, "GITHUB_RUN_ID=78", "GITHUB_REPOSITORY=o/r", "REPORT_PR=7", "GH_TOKEN=x")
	is(t, "a refused role still fails the job", r.rc, 1)
	b, _ := os.ReadFile(filepath.Join(state, "body"))
	body := string(b)
	if !(strings.Contains(body, "AWS refused the role") && strings.Contains(body, "AccessDenied") && strings.Contains(body, "repo:acme@1/infra@2:pull_request")) {
		t.Errorf("the pull request does not get a comment with the token subject and the error code: %q", body)
	}
	if strings.Contains(body, "123456789012") || strings.Contains(body, "arn:aws") || strings.Contains(body, "Not authorized to perform") {
		t.Errorf("the comment leaks the account id, ARN or AWS's message: %q", body)
	}
	if !strings.Contains(body, "<!-- tfpls:credentials -->") {
		t.Errorf("no marker (it is updated, not duplicated): %q", body)
	}
	_ = os.WriteFile(filepath.Join(state, "body"), nil, 0o644)
	run(t, f, "JSON="+jsonMap, "ROLE_ARN="+denied, "ACTIONS_ID_TOKEN_REQUEST_URL="+jwtURL, "GITHUB_RUN_ID=79", "GITHUB_REPOSITORY=o/r", "GH_TOKEN=x")
	b, _ = os.ReadFile(filepath.Join(state, "body"))
	is(t, "without REPORT_PR nothing is posted", len(b), 0)
}
