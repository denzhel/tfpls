// Package credentials exports the credentials of a job (tfpls env): a JSON map of environment variables and/or one IAM role
// assumed through GitHub OIDC. Secrets are masked before they are written, names are validated, and the choice is recorded
// in TFPLS_CREDENTIALS, the step output `credentials` and the job summary.
package credentials

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/denzhel/tfpls/internal/gh"
	"github.com/denzhel/tfpls/internal/ghactions"
)

// Env is everything Run touches outside the process.
type Env struct {
	Getenv func(string) string
	Stdout io.Writer
	GH     gh.Client
	HTTP   *http.Client // nil: a client with a 30 s timeout
}

var (
	nameRE    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	roleRE    = regexp.MustCompile(`^arn:aws[a-z-]*:iam::[0-9]{12}:role/[A-Za-z0-9+=,.@_/-]+$`)
	accountRE = regexp.MustCompile(`[0-9]{12}`)
)

const marker = "<!-- tfpls:credentials -->"

type pair struct{ key, val string }

// parseMap reads a JSON object into ordered key/value pairs; a value that is not a string is its compact JSON (tostring).
func parseMap(s string) ([]pair, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, fmt.Errorf("not an object")
	}
	var out []pair
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		v := string(raw)
		var str string
		if string(raw) != "null" && json.Unmarshal(raw, &str) == nil {
			v = str
		} else {
			var b bytes.Buffer
			if json.Compact(&b, raw) == nil {
				v = b.String()
			}
		}
		out = append(out, pair{kt.(string), v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data")
	}
	return out, nil
}

// Run returns the exit code.
func Run(e Env) int {
	r := &ghactions.Runner{Getenv: e.Getenv, Stdout: e.Stdout}
	if e.HTTP == nil {
		e.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	role, js := e.Getenv("ROLE_ARN"), e.Getenv("JSON")
	cred := "none"
	var pairs []pair
	if js != "" {
		cred = "env"
		var err error
		if pairs, err = parseMap(js); err != nil {
			r.Error("credentials JSON must be an object of KEY: value")
			return 1
		}
		for _, p := range pairs {
			if !nameRE.MatchString(p.key) {
				r.Error("invalid environment variable name in credentials JSON")
				return 1
			}
		}
		for _, p := range pairs {
			// Secrets are masked, labels and regions are not: masking short common words ("plan", "us-east-1") would turn every log
			// line containing them into ***. TFPLS_* keys, *_REGION keys and values under 6 characters stay visible.
			if !strings.HasPrefix(p.key, "TFPLS_") && !strings.HasSuffix(p.key, "_REGION") && utf8.RuneCountInString(p.val) >= 6 {
				r.Mask(p.val)
			}
			if err := r.SetEnvBlock(p.key, p.val); err != nil {
				r.Error("could not write the environment: " + err.Error())
				return 1
			}
		}
	}
	tok := ""
	if role != "" {
		if !roleRE.MatchString(role) {
			r.Error("the role is not an IAM role ARN (arn:aws:iam::<account>:role/<name>)")
			return 1
		}
		reqURL, reqTok := e.Getenv("ACTIONS_ID_TOKEN_REQUEST_URL"), e.Getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN")
		if reqURL == "" || reqTok == "" {
			r.Error("OIDC needs the job permission `id-token: write` (the caller workflow must grant it)")
			e.report("", "no OIDC token", "The caller workflow does not grant `id-token: write`.")
			return 1
		}
		aud := orDefault(e.Getenv("OIDC_AUDIENCE"), "sts.amazonaws.com")
		var err error
		if tok, err = e.oidcToken(reqURL+"&audience="+url.QueryEscape(aud), reqTok); err != nil {
			r.Error("could not get an OIDC token from GitHub")
			return 1
		}
		if tok == "" {
			r.Error("GitHub returned no OIDC token")
			return 1
		}
		r.Mask(tok)
		// where STS is: explicit input, else an endpoint from the JSON map (localstack), else AWS
		region := e.Getenv("OIDC_REGION")
		if region == "" {
			region = firstOf(pairs, "AWS_DEFAULT_REGION", "AWS_REGION")
		}
		region = orDefault(region, "us-east-1")
		endpoint := e.Getenv("STS_ENDPOINT")
		if endpoint == "" {
			endpoint = firstOf(pairs, "AWS_ENDPOINT_URL_STS", "AWS_ENDPOINT_URL")
		}
		endpoint = orDefault(endpoint, "https://sts."+region+".amazonaws.com/")
		form := url.Values{"Action": {"AssumeRoleWithWebIdentity"}, "Version": {"2011-06-15"}, "RoleArn": {role},
			"RoleSessionName":  {"tfpls-" + orDefault(e.Getenv("GITHUB_RUN_ID"), "local")},
			"WebIdentityToken": {tok}, "DurationSeconds": {orDefault(e.Getenv("OIDC_DURATION"), "3600")}}
		resp, err := e.HTTP.PostForm(endpoint, form)
		if err != nil {
			r.Error("could not reach STS at " + endpoint)
			return 1
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		f := xmlFields(body)
		ak, sk, st := f["AccessKeyId"], f["SecretAccessKey"], f["SessionToken"]
		if ak == "" || sk == "" {
			r.Error(fmt.Sprintf("STS refused to give credentials for the role (%s: %s). Check the role's trust policy (repository, branch/environment, audience).", f["Code"], f["Message"]))
			e.report(tok, "STS: "+f["Code"], "The role could not be assumed with this job's OIDC token.")
			return 1
		}
		for _, v := range []string{ak, sk, st} {
			r.Mask(v)
		}
		for _, kv := range [][2]string{{"AWS_ACCESS_KEY_ID", ak}, {"AWS_SECRET_ACCESS_KEY", sk}, {"AWS_SESSION_TOKEN", st}} {
			if kv[1] == "" {
				continue
			}
			if err := r.SetEnvBlock(kv[0], kv[1]); err != nil {
				r.Error("could not write the environment: " + err.Error())
				return 1
			}
		}
		cred = "oidc"
	}
	r.SetEnvBlock("TFPLS_CREDENTIALS", cred)
	r.SetOutput("credentials", cred)
	r.Notice("credentials: " + cred)
	r.Summary("🔑 credentials: `" + cred + "`")
	return 0
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// firstOf is `.a // .b`: the first key with a value that is not empty, null or false.
func firstOf(p []pair, keys ...string) string {
	for _, k := range keys {
		for i := len(p) - 1; i >= 0; i-- {
			if p[i].key == k {
				if v := p[i].val; v != "" && v != "null" && v != "false" {
					return v
				}
				break
			}
		}
	}
	return ""
}

func (e Env) oidcToken(u, bearer string) (string, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "bearer "+bearer)
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var v struct {
		Value string `json:"value"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v) != nil {
		return "", nil
	}
	return v.Value, nil
}

// xmlFields returns the text of the first element of each name (STS answers are small flat XML).
func xmlFields(b []byte) map[string]string {
	out := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(b))
	cur := ""
	for {
		t, err := dec.Token()
		if err != nil {
			return out
		}
		switch x := t.(type) {
		case xml.StartElement:
			cur = x.Name.Local
		case xml.EndElement:
			cur = ""
		case xml.CharData:
			if cur != "" {
				if _, ok := out[cur]; !ok {
					out[cur] = string(x)
				}
			}
		}
	}
}

// subject is the `sub` claim of a JWT, or "" when it cannot be read.
func subject(tok string) string {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var c struct {
		Sub string `json:"sub"`
	}
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	return c.Sub
}

// report explains a refused role on the pull request once: what the job presented and what to allow. It never prints the role ARN or
// AWS's message (they can carry account ids), only the error code and the token subject. Best effort.
func (e Env) report(tok, code, text string) {
	pr, repo := e.Getenv("REPORT_PR"), e.Getenv("GITHUB_REPOSITORY")
	if pr == "" || e.Getenv("GH_TOKEN") == "" || repo == "" {
		return
	}
	hint := ""
	if sub := subject(tok); sub != "" {
		base := sub
		if i := strings.LastIndex(sub, ":"); i >= 0 {
			base = sub[:i]
		}
		hint = fmt.Sprintf(" This job's token subject is `%s`: allow it (or `%s:*`) in the trust policy (`token.actions.githubusercontent.com:sub`); that also covers comments, merges and drift.", sub, base)
	}
	body := marker + "\n🔑 **AWS refused the role** (" + code + "). " + text + hint +
		" Also check that the account has the OIDC provider `token.actions.githubusercontent.com` (audience `sts.amazonaws.com`) and that the variable `TFPLS_ROLE_ARN` names the role you mean. https://github.com/" +
		orDefault(e.Getenv("GITHUB_ACTION_REPOSITORY"), "denzhel/tfpls") + "/blob/main/docs/credentials.md#the-trust-policy"
	body = accountRE.ReplaceAllString(body, "************")
	id := int64(0)
	e.GH.Pages(func(dec *json.Decoder) error {
		var cs []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
			User struct {
				Type string `json:"type"`
			} `json:"user"`
		}
		if err := dec.Decode(&cs); err != nil {
			return err
		}
		for _, c := range cs {
			if id == 0 && c.User.Type == "Bot" && strings.HasPrefix(c.Body, marker) {
				id = c.ID
			}
		}
		return nil
	}, fmt.Sprintf("repos/%s/issues/%s/comments?per_page=100", repo, pr))
	if id != 0 {
		e.GH.Run("api", "-X", "PATCH", fmt.Sprintf("repos/%s/issues/comments/%d", repo, id), "-f", "body="+body)
	} else {
		e.GH.Run("api", fmt.Sprintf("repos/%s/issues/%s/comments", repo, pr), "-f", "body="+body)
	}
}
