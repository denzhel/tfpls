#!/usr/bin/env bash
# run.sh: text from a saved plan cannot add outputs or annotations (one line), and a public repository without
# PLAN_ENCRYPTION_KEY never saves a plan (the guard sits before the packing and the action passes the visibility).
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
R="$here/actions/tofu/run.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT

# the real definitions of oneline() and out()
sed -n '/^oneline() /p;/^out() /p' "$R" > "$T/defs.sh"
GITHUB_OUTPUT="$T/gho"; : > "$GITHUB_OUTPUT"
# shellcheck disable=SC1091
. "$T/defs.sh"
out summary $'x\nstatus=applied'
eq "a newline in a value cannot add another output" "$(grep -c '^status=' "$GITHUB_OUTPUT")|$(wc -l < "$GITHUB_OUTPUT" | tr -d ' ')" "0|1"
: > "$GITHUB_OUTPUT"; out status error; out summary $'a\r\nb'
eq "CR and LF are flattened" "$(cat "$GITHUB_OUTPUT" | tr '\n' '|')" "status=error|summary=a  b|"
eq "oneline strips what would start a workflow command on a new line" "$(oneline $'ok\n::error::boom' | wc -l | tr -d ' ')" "0"

# the guard
guard=$(grep -n 'PLAN_ENCRYPTION_KEY:-}" \] && \[ "${REPO_PRIVATE:-}" != true' "$R" | head -1 | cut -d: -f1)
pack=$(grep -n 'elif tfpls_pack' "$R" | head -1 | cut -d: -f1)
{ [ -n "$guard" ] && [ -n "$pack" ] && [ "$guard" -lt "$pack" ]; } && ok "the public-repository guard comes before the plan is packed" || bad "guard missing or after tfpls_pack ($guard/$pack)"
grep -q 'REPO_PRIVATE: ${{ github.event.repository.private }}' "$here/actions/tofu/action.yml" && ok "the action passes the repository's visibility" || bad "REPO_PRIVATE not passed"
grep -q 'Plan not saved' "$R" && grep -q 'openssl rand -base64 32 | gh secret set PLAN_ENCRYPTION_KEY' "$R" && ok "the note says how to fix it" || bad "note missing"

# a held state lock is named in the summary (drift issues and alerts show it)
grep -q 'the state is locked by' "$R" && grep -q 'unlock/parse.sh' "$R" && ok "a failed plan on a held lock says who holds it and since when" || bad "lock summary missing"

# small fixes: parallelism reaches the apply too, and a plan comment that cannot be posted is an error
grep -q 'tofu apply .*-parallelism="${PARALLELISM:-10}"' "$R" && ok "apply gets -parallelism" || bad "apply without -parallelism"
[ "$(grep -c 'could not \(post\|update\) the plan comment' "$R")" = 2 ] && ok "a failed plan comment call is an ::error::" || bad "plan comment errors not reported"
eq "/tofu plan runs at 20 in parallel (it writes nothing shared)" "$(yq -r '.jobs.command.strategy."max-parallel"' "$here/.github/workflows/comment-ops.yml" | grep -c "&& 20")" "1"

done_tests
