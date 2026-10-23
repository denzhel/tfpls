#!/usr/bin/env bash
# Tests for the OIDC path of `tfpls env` against fake GitHub-OIDC and STS servers (no cloud, no network).
set -uo pipefail
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'kill ${SRV:-} 2>/dev/null; rm -rf "$T"' EXIT
build_bin tfpls "$T/tfpls" || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
E="$T/tfpls"
PORT=$((20000 + RANDOM % 20000))
cat > "$T/srv.py" <<'PY'
import http.server, sys, urllib.parse, json
port = int(sys.argv[1]); log = sys.argv[2]
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        q = urllib.parse.urlparse(self.path)
        open(log, "a").write("GET %s auth=%s\n" % (self.path, self.headers.get("Authorization")))
        if q.path == "/jwt":
            import base64
            pl = base64.urlsafe_b64encode(json.dumps({"sub": "repo:acme@1/infra@2:pull_request"}).encode()).decode().rstrip("=")
            self.send_response(200); self.end_headers(); self.wfile.write(json.dumps({"value": "h." + pl + ".s"}).encode())
        elif q.path == "/token":
            self.send_response(200); self.end_headers(); self.wfile.write(json.dumps({"value": "jwt-for-" + urllib.parse.parse_qs(q.query).get("audience", [""])[0]}).encode())
        else:
            self.send_response(404); self.end_headers()
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0)); body = urllib.parse.parse_qs(self.rfile.read(n).decode())
        open(log, "a").write("POST %s %s\n" % (self.path, json.dumps({k: v[0] for k, v in body.items()})))
        role = body.get("RoleArn", [""])[0]
        if role.endswith("/denied"):
            self.send_response(403); self.end_headers()
            self.wfile.write(b"<ErrorResponse><Error><Code>AccessDenied</Code><Message>Not authorized to perform sts:AssumeRoleWithWebIdentity</Message></Error></ErrorResponse>")
        else:
            self.send_response(200); self.end_headers()
            self.wfile.write(("<AssumeRoleWithWebIdentityResponse><AssumeRoleWithWebIdentityResult><Credentials><AccessKeyId>ASIA%s</AccessKeyId><SecretAccessKey>secret-%s</SecretAccessKey><SessionToken>token-%s</SessionToken></Credentials></AssumeRoleWithWebIdentityResult></AssumeRoleWithWebIdentityResponse>" % ("APPLY", "apply", "xyz")).encode())
http.server.HTTPServer(("127.0.0.1", port), H).serve_forever()
PY
: > "$T/srv.log"; python3 "$T/srv.py" "$PORT" "$T/srv.log" & SRV=$!
for _ in $(seq 1 30); do curl -s "http://127.0.0.1:$PORT/token?x=1" >/dev/null 2>&1 && break; sleep 0.2; done; : > "$T/srv.log"

run() { # env... ; sets out, rc ; $T/env = GITHUB_ENV
  : > "$T/env"; : > "$T/gho"
  out=$(env -i PATH="$PATH" GITHUB_ENV="$T/env" GITHUB_OUTPUT="$T/gho" GITHUB_STEP_SUMMARY=/dev/null \
    ACTIONS_ID_TOKEN_REQUEST_URL="http://127.0.0.1:$PORT/token?api=1" ACTIONS_ID_TOKEN_REQUEST_TOKEN=req-token GITHUB_RUN_ID=77 "$@" "$E" env 2>&1); rc=$?
}
envval() { awk -v k="$1" '$0 ~ "^"k"<<" {getline v; print v; exit}' "$T/env"; }
ROLE=arn:aws:iam::123456789012:role/tfpls-role
JSON="{\"AWS_DEFAULT_REGION\":\"us-east-1\",\"AWS_ENDPOINT_URL_STS\":\"http://127.0.0.1:$PORT/\"}"

run JSON="$JSON" ROLE_ARN="$ROLE"
eq "with a role: short-lived keys from STS" "$rc|$(envval AWS_ACCESS_KEY_ID)|$(envval AWS_SECRET_ACCESS_KEY)|$(envval AWS_SESSION_TOKEN)" "0|ASIAAPPLY|secret-apply|token-xyz"
eq "labelled oidc" "$(envval TFPLS_CREDENTIALS)|$(sed -n 's/^credentials=//p' "$T/gho")" "oidc|oidc"
eq "the keys are masked in the log" "$(grep -c '::add-mask::secret-apply' <<<"$out")|$(grep -c '::add-mask::jwt-for-' <<<"$out")" "1|1"
eq "the OIDC token was requested with the AWS audience and the runner's bearer token" "$(grep -c 'GET /token?api=1&audience=sts.amazonaws.com auth=bearer req-token' "$T/srv.log")" 1
eq "STS got the role, the web identity token and a session name with the run id" "$(grep POST "$T/srv.log" | sed 's/^POST [^ ]* //' | jq -r '[.RoleArn,.WebIdentityToken,.RoleSessionName,.Action]|join("|")')" "$ROLE|jwt-for-sts.amazonaws.com|tfpls-77|AssumeRoleWithWebIdentity"

: > "$T/srv.log"; run ROLE_ARN="$ROLE" JSON='{"AWS_ACCESS_KEY_ID":"static-key-12345"}' STS_ENDPOINT="http://127.0.0.1:$PORT/"
lastval() { awk -v k="$1" '$0 ~ "^"k"<<" {getline v; r=v} END{print r}' "$T/env"; }
eq "the role's keys are written after the JSON map, so they win (the last value of an env file counts)" "$rc|$(lastval AWS_ACCESS_KEY_ID)" "0|ASIAAPPLY"
: > "$T/srv.log"; run JSON="$JSON" ROLE_ARN="$ROLE" OIDC_AUDIENCE=my-audience
eq "a custom audience is passed on" "$(grep -c 'audience=my-audience' "$T/srv.log")" 1

# failures stop the job with a message
run JSON="$JSON" ROLE_ARN="arn:aws:iam::123456789012:role/denied"
eq "STS refusing the role fails with the reason and a trust-policy hint" "$rc|$(grep -c 'AccessDenied: Not authorized' <<<"$out")|$(grep -c 'trust policy' <<<"$out")|$(envval AWS_ACCESS_KEY_ID)" "1|1|1|"
run JSON="$JSON" ROLE_ARN="$ROLE" ACTIONS_ID_TOKEN_REQUEST_URL=""
eq "without id-token: write the error says so" "$rc|$(grep -c 'id-token: write' <<<"$out")" "1|1"
run JSON="$JSON" ROLE_ARN="not-an-arn"
eq "a malformed role is refused before anything is requested" "$rc|$(grep -c 'not an IAM role ARN' <<<"$out")" "1|1"
run JSON="$JSON" ROLE_ARN="$ROLE" STS_ENDPOINT="http://127.0.0.1:1/"
eq "an unreachable STS fails clearly" "$rc|$(grep -c 'could not reach STS' <<<"$out")" "1|1"

# no role: the old behaviour is untouched
run JSON='{"AWS_ACCESS_KEY_ID":"static-key-12345"}'
eq "no role: the static keys of the JSON map" "$rc|$(envval AWS_ACCESS_KEY_ID)|$(envval TFPLS_CREDENTIALS)" "0|static-key-12345|env"

# a refused role is explained on the pull request: once, with the token subject, never the role ARN or AWS's message
mkdir -p "$T/bin"; cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
echo "$*" >> "$STUB_LOG"
case "$*" in
  *"-X PATCH"*|*"issues/"*"/comments -f"*|*"/comments -f body"*) for a in "$@"; do case "$a" in body=*) printf '%s\n' "${a#body=}" >> "$STUB_BODY";; esac; done; echo '{}';;
  *) echo '[]';;
esac
STUB
chmod +x "$T/bin/gh"; : > "$T/ghlog"; : > "$T/ghbody"
out=$(env -i PATH="$T/bin:$PATH" STUB_LOG="$T/ghlog" STUB_BODY="$T/ghbody" GITHUB_ENV="$T/env" GITHUB_OUTPUT="$T/gho" GITHUB_STEP_SUMMARY=/dev/null GITHUB_REPOSITORY=o/r \
  ACTIONS_ID_TOKEN_REQUEST_URL="http://127.0.0.1:$PORT/jwt?api=1" ACTIONS_ID_TOKEN_REQUEST_TOKEN=req-token GITHUB_RUN_ID=78 \
  JSON="$JSON" ROLE_ARN="arn:aws:iam::123456789012:role/denied" REPORT_PR=7 GH_TOKEN=x "$E" env 2>&1); rc=$?
eq "a refused role still fails the job" "$rc" 1
body=$(cat "$T/ghbody")
case "$body" in *"AWS refused the role"*"AccessDenied"*"repo:acme@1/infra@2:pull_request"*) ok "the pull request gets one comment with the token subject and the error code";; *) bad "comment: $body";; esac
case "$body" in *123456789012*|*"arn:aws"*|*"Not authorized to perform"*) bad "the comment leaks the account id, ARN or AWS's message: $body";; *) ok "the comment has no account id, no role ARN and no AWS message";; esac
case "$body" in *"<!-- tfpls:credentials -->"*) ok "it carries its own marker (updated, not duplicated)";; *) bad "no marker";; esac
: > "$T/ghbody"; env -i PATH="$T/bin:$PATH" STUB_LOG="$T/ghlog" STUB_BODY="$T/ghbody" GITHUB_ENV="$T/env" GITHUB_OUTPUT="$T/gho" GITHUB_STEP_SUMMARY=/dev/null GITHUB_REPOSITORY=o/r \
  ACTIONS_ID_TOKEN_REQUEST_URL="http://127.0.0.1:$PORT/jwt?api=1" ACTIONS_ID_TOKEN_REQUEST_TOKEN=req-token GITHUB_RUN_ID=79 JSON="$JSON" ROLE_ARN="arn:aws:iam::123456789012:role/denied" GH_TOKEN=x "$E" env >/dev/null 2>&1
eq "without REPORT_PR nothing is posted" "$(wc -c < "$T/ghbody" | tr -d ' ')" 0

done_tests
