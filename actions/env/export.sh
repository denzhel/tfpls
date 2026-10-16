#!/usr/bin/env bash
# Export the credentials for this job: a JSON map of environment variables (JSON) and/or one IAM role assumed through
# GitHub OIDC (ROLE_ARN). Every job uses the same credentials.
# Secret values are masked (not TFPLS_* labels, *_REGION keys or values under 6 chars), keys must be valid env names.
# Records the choice in TFPLS_CREDENTIALS (env | oidc | none), the step output `credentials`, and the job summary.
set -uo pipefail

GH_ENV="${GITHUB_ENV:-/dev/null}"
GH_OUT="${GITHUB_OUTPUT:-/dev/null}"
GH_SUM="${GITHUB_STEP_SUMMARY:-/dev/null}"

# When a pull request (or issue) number is given, a refused role is explained there once: what the job presented and what to allow.
# Never prints the role ARN or AWS's message (they can carry account ids); only the error code and the token subject.
report() { # <error code> <text of the hint>
  [ -n "${REPORT_PR:-}" ] && [ -n "${GH_TOKEN:-}" ] && [ -n "${GITHUB_REPOSITORY:-}" ] || return 0
  local sub body id marker="<!-- tfpls:credentials -->"
  sub=$(jq -rR 'split(".")[1] // empty | gsub("-";"+") | gsub("_";"/") | @base64d | fromjson | .sub // empty' <<<"${tok:-}" 2>/dev/null)
  local hint=""
  if [ -n "$sub" ]; then
    hint=" This job's token subject is \`$sub\`: allow it (or \`${sub%:*}:*\`) in the trust policy (\`token.actions.githubusercontent.com:sub\`); that also covers comments, merges and drift."
  fi
  body="$marker
🔑 **AWS refused the role** ($1). $2$hint Also check that the account has the OIDC provider \`token.actions.githubusercontent.com\` (audience \`sts.amazonaws.com\`) and that the variable \`TFPLS_ROLE_ARN\` names the role you mean. https://github.com/${GITHUB_ACTION_REPOSITORY:-denzhel/tfpls}/blob/main/docs/credentials.md#the-trust-policy"
  body=$(printf '%s' "$body" | sed -E 's/[0-9]{12}/************/g')
  id=$(gh api --paginate "repos/$GITHUB_REPOSITORY/issues/$REPORT_PR/comments?per_page=100" 2>/dev/null | jq -s -r --arg m "$marker" 'add // [] | .[] | select(.user.type == "Bot" and (.body | startswith($m))) | .id' | head -1)
  if [ -n "$id" ]; then gh api -X PATCH "repos/$GITHUB_REPOSITORY/issues/comments/$id" -f body="$body" >/dev/null 2>&1 || true
  else gh api "repos/$GITHUB_REPOSITORY/issues/$REPORT_PR/comments" -f body="$body" >/dev/null 2>&1 || true; fi
}

ROLE="${ROLE_ARN:-}"
json="${JSON:-}"; cred="none"; [ -n "$json" ] && cred="env"

if [ -n "$json" ]; then
  jq -e 'type == "object"' <<<"$json" >/dev/null 2>&1 || { echo "::error::credentials JSON must be an object of KEY: value"; exit 1; }
  while IFS=$'\t' read -r k v64; do
    [[ "$k" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || { echo "::error::invalid environment variable name in credentials JSON"; exit 1; }
    v=$(printf '%s' "$v64" | base64 -d)
    # Mask secrets, but not labels/regions: masking short common words ("plan", "us-east-1") turns every log line
    # containing them into ***. TFPLS_* keys, *_REGION keys and values shorter than 6 characters are left visible.
    mask=true
    case "$k" in TFPLS_*|*_REGION) mask=false;; esac
    [ "${#v}" -lt 6 ] && mask=false
    if [ "$mask" = true ]; then
      printf '%s\n' "$v" | while IFS= read -r line; do [ -n "$line" ] && echo "::add-mask::$line"; done
    fi
    d="__TFPLS_EOF_${$}_${RANDOM}__"
    { printf '%s<<%s\n' "$k" "$d"; printf '%s\n' "$v"; printf '%s\n' "$d"; } >> "$GH_ENV"
  done < <(jq -r 'to_entries[] | [.key, (.value | tostring | @base64)] | @tsv' <<<"$json")
fi

if [ -n "$ROLE" ]; then
  [[ "$ROLE" =~ ^arn:aws[a-z-]*:iam::[0-9]{12}:role/[A-Za-z0-9+=,.@_/-]+$ ]] || { echo "::error::the role is not an IAM role ARN (arn:aws:iam::<account>:role/<name>)"; exit 1; }
  if [ -z "${ACTIONS_ID_TOKEN_REQUEST_URL:-}" ] || [ -z "${ACTIONS_ID_TOKEN_REQUEST_TOKEN:-}" ]; then
    echo "::error::OIDC needs the job permission \`id-token: write\` (the caller workflow must grant it)"; report "no OIDC token" "The caller workflow does not grant \`id-token: write\`."; exit 1
  fi
  aud="${OIDC_AUDIENCE:-sts.amazonaws.com}"
  tok=$(curl -fsS -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" "${ACTIONS_ID_TOKEN_REQUEST_URL}&audience=${aud}" | jq -r '.value // empty') \
    || { echo "::error::could not get an OIDC token from GitHub"; exit 1; }
  [ -n "$tok" ] || { echo "::error::GitHub returned no OIDC token"; exit 1; }
  echo "::add-mask::$tok"
  # where STS is: explicit input, else an endpoint from the JSON map (localstack), else AWS
  region="${OIDC_REGION:-}"; [ -n "$region" ] || region=$(jq -r '.AWS_DEFAULT_REGION // .AWS_REGION // empty' <<<"${json:-{\}}" 2>/dev/null)
  region="${region:-us-east-1}"
  endpoint="${STS_ENDPOINT:-}"
  [ -n "$endpoint" ] || endpoint=$(jq -r '.AWS_ENDPOINT_URL_STS // .AWS_ENDPOINT_URL // empty' <<<"${json:-{\}}" 2>/dev/null)
  endpoint="${endpoint:-https://sts.${region}.amazonaws.com/}"
  resp=$(curl -sS -X POST "$endpoint" --data-urlencode "Action=AssumeRoleWithWebIdentity" --data-urlencode "Version=2011-06-15" \
          --data-urlencode "RoleArn=$ROLE" --data-urlencode "RoleSessionName=tfpls-${GITHUB_RUN_ID:-local}" \
          --data-urlencode "WebIdentityToken=$tok" --data-urlencode "DurationSeconds=${OIDC_DURATION:-3600}") \
    || { echo "::error::could not reach STS at $endpoint"; exit 1; }
  field() { sed -n "s:.*<$1>\([^<]*\)</$1>.*:\1:p" <<<"$resp" | head -1; }
  ak=$(field AccessKeyId); sk=$(field SecretAccessKey); st=$(field SessionToken)
  if [ -z "$ak" ] || [ -z "$sk" ]; then
    echo "::error::STS refused to give credentials for the role ($(field Code): $(field Message)). Check the role's trust policy (repository, branch/environment, audience)."
    report "STS: $(field Code)" "The role could not be assumed with this job's OIDC token."
    exit 1
  fi
  for v in "$ak" "$sk" "$st"; do [ -n "$v" ] && echo "::add-mask::$v"; done
  d="__TFPLS_EOF_${$}_${RANDOM}__"
  { printf 'AWS_ACCESS_KEY_ID<<%s\n%s\n%s\n' "$d" "$ak" "$d"; printf 'AWS_SECRET_ACCESS_KEY<<%s\n%s\n%s\n' "$d" "$sk" "$d"
    [ -n "$st" ] && printf 'AWS_SESSION_TOKEN<<%s\n%s\n%s\n' "$d" "$st" "$d"; } >> "$GH_ENV"
  cred="oidc"
fi

{ printf 'TFPLS_CREDENTIALS<<__TFPLS_C__\n%s\n__TFPLS_C__\n' "$cred"; } >> "$GH_ENV"
echo "credentials=$cred" >> "$GH_OUT"
echo "::notice::credentials: $cred"
echo "🔑 credentials: \`$cred\`" >> "$GH_SUM"
