#!/usr/bin/env bash
# Static checks of the single entry workflow .github/workflows/tofu.yml: routing, secrets, and that every input it forwards exists.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
W="$here/.github/workflows"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
E="$W/tofu.yml"

jobs=$(yq -r '.jobs | keys | .[]' "$E" | sort | tr '\n' ' ')
eq "the entry workflow has the seven routes" "$jobs" "apply comment dashboard drift fix locks plan "
route() { yq -r ".jobs.$1.uses" "$E" | sed -E 's#.*/workflows/([a-z-]+)\.yml@.*#\1#'; }
eq "pull_request -> plan.yml" "$(route plan)|$(yq -r .jobs.plan.if "$E")" "plan|github.event_name == 'pull_request'"
eq "issue_comment on a pull request -> comment-ops.yml" "$(route comment)|$(yq -r .jobs.comment.if "$E")" "comment-ops|github.event_name == 'issue_comment' && github.event.action == 'created' && github.event.issue.pull_request"
eq "issue_comment on an issue and the fix task -> drift-fix.yml" "$(route fix)|$(yq -r .jobs.fix.if "$E" | grep -c "!github.event.issue.pull_request")|$(yq -r .jobs.fix.if "$E" | grep -c "task == 'fix'")" "drift-fix|1|1"
eq "push -> apply.yml" "$(route apply)|$(yq -r .jobs.apply.if "$E")" "apply|github.event_name == 'push' && github.ref == format('refs/heads/{0}', github.event.repository.default_branch)"
eq "schedule and dispatch -> drift.yml" "$(route drift)|$(yq -r .jobs.drift.if "$E" | grep -c "'schedule'")" "drift|1"
eq "locks.yml only by the manual task, never on the schedule" "$(route locks)|$(yq -r .jobs.locks.if "$E" | grep -c "'schedule'")|$(yq -r .jobs.locks.if "$E" | grep -c "task == 'locks'")" "locks|0|1"
eq "dashboard refresh after drift" "$(route dashboard)|$(yq -r '.jobs.dashboard.needs|join(",")' "$E")" "dashboard|drift"

eq "every route passes the secrets on" "$(for j in $jobs; do yq -r ".jobs.$j.secrets" "$E"; done | sort -u)" "inherit"
# every input forwarded to a building block must exist there, and every entry input must be used somewhere
for j in plan comment apply drift locks dashboard fix; do
  target=$(route "$j")
  missing=""
  for k in $(yq -r ".jobs.$j.with | keys | .[]" "$E"); do
    yq -e ".on.workflow_call.inputs | has(\"$k\")" "$W/$target.yml" >/dev/null 2>&1 || missing+="$k "
  done
  eq "$j -> $target.yml: every forwarded input exists there" "$missing" ""
done
unused=""
for k in $(yq -r '.on.workflow_call.inputs | keys | .[]' "$E"); do grep -q "inputs\.$k\b" <(yq -r '.jobs' "$E" | cat) || unused+="$k "; done
eq "every input of the entry workflow is forwarded" "$unused" ""
eq "no building block is referenced by a local path (callers are in other repositories)" "$(grep -c 'uses: \./' "$E")" 0
# one default per switch in every workflow that has it
for k in audit drift_issues; do
  vals=$(for f in tofu plan apply comment-ops drift locks; do yq -r ".on.workflow_call.inputs.$k.default // \"-\"" "$W/$f.yml"; done | grep -v '^-$' | sort -u | tr '\n' ' ')
  eq "$k has the same default in every workflow" "$vals" "true "
done
eq "the defaults of the production profile" "$(yq -r '[.on.workflow_call.inputs.audit.default, .on.workflow_call.inputs.drift_issues.default, .on.workflow_call.inputs.queue.default]|join(",")' "$E")" "true,true,false"

# the approval rules and CODEOWNERS come from the default branch, not from the pull request's own config
C="$W/comment-ops.yml"
eq "the approvals step reads the rules from the default branch" "$(yq -r '.jobs.parse.steps[] | select(.name == "Approvals") | .with.default_branch' "$C")" '${{ github.event.repository.default_branch }}'
eq "the CODEOWNERS step reads CODEOWNERS from the default branch" "$(yq -r '.jobs.parse.steps[] | select(.name == "CODEOWNERS approvals") | .with.base' "$C")" '${{ github.event.repository.default_branch }}'
eq "CODEOWNERS paths come from the approvals step (rules of main plus the pull request's)" "$(yq -r '.jobs.parse.steps[] | select(.name == "CODEOWNERS approvals") | .with.entries' "$C")" '${{ steps.a.outputs.owner_entries }}'
eq "the policy settings of the apply come from the approvals step" "$(yq -r '.jobs.parse.outputs.entries' "$C")" '${{ steps.a.outputs.entries || steps.d.outputs.entries }}'

eq "/tofu unlock checks out the default branch, not the pull request (parse job)" "$(yq -r '.jobs.parse.steps[] | select(.uses != null and (.uses | test("actions/checkout"))) | .with.ref' "$C")" "\${{ steps.p.outputs.cmd == 'unlock' && github.event.repository.default_branch || steps.p.outputs.sha }}"
eq "/tofu unlock runs the default branch's code (command job)" "$(yq -r '.jobs.command.steps[] | select(.uses != null and (.uses | test("actions/prepare"))) | .with.ref' "$C")" "\${{ needs.parse.outputs.cmd == 'unlock' && github.event.repository.default_branch || needs.parse.outputs.sha }}"
eq "the triage workflow gives Claude no raw gh api" "$(grep -c 'allowedTools.*gh api' "$W/claude-triage.yml")" "0"

done_tests
