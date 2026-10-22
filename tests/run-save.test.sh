#!/usr/bin/env bash
# The wiring of the tofu action's plan-saving inputs (the behaviour is tests/tofu-run.test.sh and internal/tofu): the action passes
# the repository's visibility and allow_unencrypted_plans, and the workflows forward them.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
grep -q 'REPO_PRIVATE: ${{ github.event.repository.private }}' "$here/actions/tofu/action.yml" && ok "the action passes the repository's visibility" || bad "REPO_PRIVATE not passed"

eq "the action input defaults to false" "$(yq -r '.inputs.allow_unencrypted_plans.default' "$here/actions/tofu/action.yml")" "false"
grep -q 'ALLOW_UNENCRYPTED_PLANS: ${{ inputs.allow_unencrypted_plans }}' "$here/actions/tofu/action.yml" && ok "the action passes the input to the engine" || bad "env not wired in action.yml"
for f in tofu plan comment-ops drift-fix; do
  eq "$f.yml declares allow_unencrypted_plans (boolean, default false)" "$(yq -r '.on.workflow_call.inputs.allow_unencrypted_plans | .type + "/" + (.default|tostring)' "$here/.github/workflows/$f.yml")" "boolean/false"
done
for f in plan comment-ops drift-fix; do
  eq "$f.yml passes it to the tofu action" "$(grep -c 'allow_unencrypted_plans: ${{ inputs.allow_unencrypted_plans }}' "$here/.github/workflows/$f.yml")" "1"
done
eq "tofu.yml passes it to the three workflows that save plans" "$(grep -c 'allow_unencrypted_plans: ${{ inputs.allow_unencrypted_plans }}' "$here/.github/workflows/tofu.yml")" "3"

eq "/tofu plan runs at 20 in parallel (it writes nothing shared)" "$(yq -r '.jobs.command.strategy."max-parallel"' "$here/.github/workflows/comment-ops.yml" | grep -c "&& 20")" "1"

done_tests
