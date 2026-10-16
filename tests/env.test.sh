#!/usr/bin/env bash
# Offline tests for actions/env/export.sh. Run: bash tests/env.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
S="$here/../actions/env/export.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT

# run <name> <env assignments...>  -> sets rc, OUT (stdout), and files $T/env $T/out $T/sum
run() {
  : > "$T/env"; : > "$T/out"; : > "$T/sum"
  OUT=$(env -i PATH="$PATH" GITHUB_ENV="$T/env" GITHUB_OUTPUT="$T/out" GITHUB_STEP_SUMMARY="$T/sum" "$@" bash "$S" 2>&1); rc=$?
}
has()  { grep -q -- "$1" "$T/env"; }
CRED='{"AWS_ACCESS_KEY_ID":"the-key","TFPLS_CRED_KIND":"one"}'

run JSON="$CRED"
[ $rc -eq 0 ] && has "TFPLS_CRED_KIND" && grep -q '^one$' <(sed -n '/TFPLS_CRED_KIND<</{n;p;}' "$T/env") && ok "the JSON map is exported" || bad "the JSON map is exported"
grep -q 'credentials=env' "$T/out" && ok "output credentials=env" || bad "output env"
grep -q 'TFPLS_CREDENTIALS<<' "$T/env" && ok "TFPLS_CREDENTIALS exported" || bad "TFPLS_CREDENTIALS"
run JSON='{"BAD-KEY":"x"}'
[ $rc -ne 0 ] && ok "invalid env name rejected" || bad "invalid env name"
run JSON='["not","an","object"]'
[ $rc -ne 0 ] && ok "non-object JSON rejected" || bad "non-object JSON"

run JSON='{"SECRET":"s3cr3t-value"}'
case "$OUT" in *"::add-mask::s3cr3t-value"*) ok "values are masked";; *) bad "masking: $OUT";; esac
case "$OUT" in *"s3cr3t-value"*"s3cr3t-value"*) bad "value printed outside the mask command";; *) ok "value appears only in the mask command";; esac

run JSON='{"TFPLS_CRED_KIND":"apply","AWS_REGION":"us-east-1","SHORT":"abc","TOKEN":"long-secret-token"}'
case "$OUT" in *"::add-mask::apply"*|*"::add-mask::us-east-1"*|*"::add-mask::abc"*) bad "labels/regions/short values must not be masked";; *) ok "labels, regions and short values are not masked";; esac
case "$OUT" in *"::add-mask::long-secret-token"*) ok "long secret still masked";; *) bad "long secret masked: $OUT";; esac
case "$OUT" in *"credentials: env"*) ok "credentials notice stays readable";; *) bad "notice: $OUT";; esac

run JSON="$(jq -cn --arg v $'line1\nline2' '{PEM:$v}')"
[ "$(sed -n '/^PEM<</,/^__TFPLS_EOF/p' "$T/env" | sed '1d;$d' | tr '\n' '|')" = "line1|line2|" ] && ok "multi-line values survive" || bad "multi-line value"

run JSON=""
[ $rc -eq 0 ] && grep -q 'credentials=none' "$T/out" && ok "empty credentials are allowed (ambient auth)" || bad "empty creds"
grep -q 'credentials: `none`' "$T/sum" && ok "job summary records the kind" || bad "summary"

done_tests
