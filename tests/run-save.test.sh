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

# the apply hint appears once, under the plan (format.sh); the saved-plan line above it is gone, the unencrypted warning stays
grep -q 'Reviewed plan saved' "$R" && bad "run.sh still adds the 'Reviewed plan saved' line" || ok "no duplicate 'Reviewed plan saved' line above the plan"
grep -qF 'is not set: the saved plan is stored unencrypted' "$R" && ok "the unencrypted-plan warning stays" || bad "unencrypted warning missing"

# the apply comment of a reviewed plan has no "Applying the reviewed plan" line; the run log and the job summary still name the plan
grep -q 'Applying the reviewed plan' "$R" && bad "run.sh still puts 'Applying the reviewed plan' into the comment" || ok "no 'Applying the reviewed plan' line in the apply comment"
grep -q '::notice::Using the reviewed plan' "$R" && grep -q 'plan_note="\*\*$NAME\*\*: used the reviewed plan' "$R" && ok "the run log and the job summary still name the reviewed plan" || bad "notice or plan_note missing"

# allow_unencrypted_plans: the opt-in only silences the "not encrypted" warning; the public-repository guard does not read it
. "$here/actions/tofu/format.sh"
for v in "" false FALSE 1 yes; do tfpls_unencrypted_warns "$v" && w="warns" || w="quiet"; eq "opt-in '$v' keeps the warning" "$w" "warns"; done
tfpls_unencrypted_warns true && bad "opt-in true still warns" || ok "opt-in true silences the warning"
grep -q 'tfpls_unencrypted_warns "${ALLOW_UNENCRYPTED_PLANS:-false}"' "$R" && ok "run.sh asks the helper before it warns" || bad "run.sh does not use the helper"
guardline=$(grep -n 'PLAN_ENCRYPTION_KEY:-}" \] && \[ "${REPO_PRIVATE:-}" != true' "$R" | head -1)
case "$guardline" in *ALLOW_UNENCRYPTED_PLANS*) bad "the public-repository guard reads the opt-in";; *) ok "the public-repository guard does not read the opt-in (public and unknown visibility never save unencrypted)";; esac
[ "$(grep -c 'ALLOW_UNENCRYPTED_PLANS' "$R")" = 1 ] && ok "the opt-in is used in exactly one place of run.sh" || bad "ALLOW_UNENCRYPTED_PLANS used $(grep -c 'ALLOW_UNENCRYPTED_PLANS' "$R") times"
eq "the action input defaults to false" "$(yq -r '.inputs.allow_unencrypted_plans.default' "$here/actions/tofu/action.yml")" "false"
grep -q 'ALLOW_UNENCRYPTED_PLANS: ${{ inputs.allow_unencrypted_plans }}' "$here/actions/tofu/action.yml" && ok "the action passes the input to run.sh" || bad "env not wired in action.yml"
for f in tofu plan comment-ops drift-fix; do
  eq "$f.yml declares allow_unencrypted_plans (boolean, default false)" "$(yq -r '.on.workflow_call.inputs.allow_unencrypted_plans | .type + "/" + (.default|tostring)' "$here/.github/workflows/$f.yml")" "boolean/false"
done
for f in plan comment-ops drift-fix; do
  eq "$f.yml passes it to the tofu action" "$(grep -c 'allow_unencrypted_plans: ${{ inputs.allow_unencrypted_plans }}' "$here/.github/workflows/$f.yml")" "1"
done
eq "tofu.yml passes it to the three workflows that save plans" "$(grep -c 'allow_unencrypted_plans: ${{ inputs.allow_unencrypted_plans }}' "$here/.github/workflows/tofu.yml")" "3"

# a held state lock is named in the summary (drift issues and alerts show it)
grep -q 'the state is locked by' "$R" && grep -q 'unlock/parse.sh' "$R" && ok "a failed plan on a held lock says who holds it and since when" || bad "lock summary missing"

# small fixes: parallelism reaches the apply too, and a plan comment that cannot be posted is an error
grep -q 'tofu apply .*-parallelism="${PARALLELISM:-10}"' "$R" && ok "apply gets -parallelism" || bad "apply without -parallelism"
grep -q 'tfpls_post_comment "$marker" "$body"' "$R" && grep -q '::error::could not post the plan comment' "$here/actions/tofu/post.sh" && ok "a failed plan comment call is an ::error::" || bad "plan comment errors not reported"
eq "/tofu plan runs at 20 in parallel (it writes nothing shared)" "$(yq -r '.jobs.command.strategy."max-parallel"' "$here/.github/workflows/comment-ops.yml" | grep -c "&& 20")" "1"

done_tests
