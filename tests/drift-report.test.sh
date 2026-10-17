#!/usr/bin/env bash
# Tests for actions/drift-report/report.sh: transitions and the issue lifecycle, with a local bare repo and a gh stub.
set -uo pipefail
export TFPLS_RETRY_SLEEP=0   # retries pause randomly in the scripts under test; the tests do not wait
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
R="$here/actions/drift-report/report.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
git init -q --bare "$T/remote.git"; mkdir -p "$T/bin"
cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
echo "$*" >> "$STUB_LOG"
body=; prev=; for a in "$@"; do [ "$prev" = --body ] && body=$a; prev=$a; done
[ -n "$body" ] && printf '%s' "$body" > "$STUB_BODY"
case "$1 $2" in
  "issue create") echo "https://github.com/o/r/issues/$((100 + $(grep -c '^issue create' "$STUB_LOG")))";;
  "issue list") if [ -n "${STUB_EXISTING:-}" ]; then echo "[{\"number\":${STUB_EXISTING},\"title\":\"Drift: ${STUB_EXISTING_TITLE:-x}\"}]"; else echo "[]"; fi;;
  "issue view") grep -q "^issue close $3 " "$STUB_LOG" && echo CLOSED || echo OPEN;;
esac
exit 0
STUB
chmod +x "$T/bin/gh"
export TFPLS_REMOTE_URL="$T/remote.git" STUB_LOG="$T/log" STUB_BODY="$T/body" PATH="$T/bin:$PATH" REPO=o/r RUN_URL=http://run BRANCH=tfpls-data GH_TOKEN=x
step() { # name state -> transition issue
  : > "$T/gho"
  NAME="$1" STATE="$2" SUMMARY="+0 ~1 -0" GITHUB_OUTPUT="$T/gho" TFPLS_NOW="${3:-2026-10-10T10:00:00Z}" bash "$R" >"$T/out" 2>&1
  tr_=$(sed -n 's/^transition=//p' "$T/gho"); is_=$(sed -n 's/^issue=//p' "$T/gho")
}
n() { grep -c "^$1" "$T/log"; }
st() { git --git-dir="$T/remote.git" show "tfpls-data:drift/$1.json" 2>/dev/null | jq -r .state; }

: > "$T/log"
step terraform/dev in-sync; eq "first sight of an in-sync workspace: no transition, no issue" "$tr_|$(n 'issue create')" "none|0"
step terraform/dev drift; eq "in-sync -> drift is a transition and opens one issue" "$tr_|$is_|$(n 'issue create')" "drifted|101|1"
B="$T/body"
eq "the issue body has no run link and none of the how-to text" "$(grep -c 'Latest run\|Fix it without a pull request\|/tofu fix\|To change the code instead\|opened when a workspace drifts' "$B")" "0"
eq "without a plan text the issue still has the title line and the table, and no collapsed block" "$(grep -c 'has drifted from its code' "$B")|$(grep -c '^| Since |' "$B")|$(grep -c '| Plan | +0 ~1 -0 |' "$B")|$(grep -c '<details>' "$B")" "1|1|1|0"
step terraform/dev drift 2026-10-10T11:00:00Z; eq "still drifted: no transition, issue refreshed not duplicated" "$tr_|$(n 'issue create')|$(n 'issue edit')" "none|1|1"
eq "the since date survives a repeated check" "$(git --git-dir="$T/remote.git" show tfpls-data:drift/terraform_dev.json | jq -r .since)" "2026-10-10T10:00:00Z"
step terraform/dev drift 2026-10-10T12:00:00Z; eq "a third check is still quiet" "$tr_|$(n 'issue create')" "none|1"
step terraform/dev in-sync; eq "drift -> in-sync is recovered and closes the issue" "$tr_|$(n 'issue close')|$(st terraform_dev)" "recovered|1|in-sync"
step terraform/dev in-sync; eq "in-sync again: nothing" "$tr_|$(n 'issue close')" "none|1"
step terraform/dev drift; eq "drifting again opens a fresh issue" "$tr_|$is_|$(n 'issue create')" "drifted|102|2"
step terraform/dev error; eq "drift -> error is reported once as failing and comments on the issue" "$tr_|$(n 'issue comment')" "failing|1"
step terraform/dev error; eq "error again: quiet" "$tr_|$(n 'issue comment')" "none|1"

: > "$T/log"
step terraform/prod error; eq "in-sync -> error is failing but opens no issue" "$tr_|$(n 'issue create')" "failing|0"
step terraform/prod in-sync; eq "error -> in-sync is recovered" "$tr_" "recovered"
ISSUES=false step terraform/stg drift; eq "issues: false: a newly seen drift is still reported, with no issue" "$tr_|$(n issue)" "drifted|0"
step terraform/stg2 in-sync >/dev/null; ISSUES=false step terraform/stg2 drift; eq "issues: false: transition is reported" "$tr_|$(n 'issue')" "drifted|0"

# the plan in the issue, laid out like a plan comment
PT=$'Terraform will perform the following actions:\n\n  # aws_sqs_queue.a will be created\n  + resource "aws_sqs_queue" "a" {}\n\nPlan: 1 to add, 0 to change, 0 to destroy.'
: > "$T/log"; step terraform/pl in-sync >/dev/null
PLAN_TEXT="$PT" step terraform/pl drift
eq "a drift issue with a plan text: Plan line, one collapsed block, the plan inside" "$(grep -c '^\*\*Plan:\*\* 1 to add, 0 to change, 0 to destroy\.$' "$B")|$(grep -c '<details><summary>Click here to expand</summary>' "$B")|$(grep -c 'will be created' "$B")|$(grep -c '^</details>$' "$B")" "1|1|1|1"
eq "that body has no run link and no how-to text either" "$(grep -c 'Latest run\|Fix it without a pull request\|/tofu fix' "$B")" "0"
[ "$(sed -n 1p "$B")" = '**`terraform/pl`** has drifted from its code.' ] && ok "the title line comes first" || bad "title line: $(sed -n 1p "$B")"
PLAN_TEXT=$'  # aws_sqs_queue.b will be created\n\nPlan: 2 to add, 0 to change, 0 to destroy.' step terraform/pl drift 2026-10-10T11:00:00Z
eq "a refresh of the open issue replaces the plan with the newest one" "$(grep -c 'Plan:\*\* 2 to add' "$B")|$(grep -c 'aws_sqs_queue.b' "$B")|$(grep -c 'aws_sqs_queue.a' "$B")|$(n 'issue create')" "1|1|0|1"
BIG=$(for i in $(seq 1 6000); do echo "  # resource number $i will be created with some attribute = \"value-$i\""; done; echo "Plan: 6000 to add, 0 to change, 0 to destroy.")
PLAN_TEXT="$BIG" step terraform/pl drift 2026-10-10T12:00:00Z
[ "$(wc -c < "$B" | tr -d ' ')" -lt 62000 ] && grep -q '^\*\*Plan:\*\* 6000 to add' "$B" && grep -q 'bytes omitted' "$B" && grep -q '^Plan: 6000 to add' "$B" && ok "a huge plan is clipped to fit an issue; the summary line is kept" || bad "huge plan: $(wc -c < "$B") bytes"
PLAN_TEXT="" step terraform/pl drift 2026-10-10T13:00:00Z
eq "an empty plan text leaves the table only (the run does not fail)" "$tr_|$(grep -c '<details>' "$B")|$(grep -c '^| Since |' "$B")" "none|0|1"
grep -q 'tfpls_clip_plan\|tfpls_plan_line' "$R" && ok "report.sh reuses the plan-comment helpers of format.sh" || bad "helpers not reused"

# the real outml() of run.sh: a plan line cannot end the output early or add another output
sed -n '/^outml() /p' "$here/actions/tofu/run.sh" > "$T/outml.sh"
GITHUB_OUTPUT="$T/gho2"; : > "$GITHUB_OUTPUT"
# shellcheck disable=SC1091
. "$T/outml.sh"
outml plan_text $'line one\nghost=1\nTFPLS_deadbeef\nlast line'
first=$(head -1 "$GITHUB_OUTPUT"); lastl=$(tail -1 "$GITHUB_OUTPUT"); delim=${first#plan_text<<}
{ [ "${first%%<<*}" = plan_text ] && [ "$lastl" = "$delim" ] && [ "$(grep -cF "$delim" "$GITHUB_OUTPUT")" = 2 ]; } && ok "a multi-line output is wrapped in a delimiter that occurs only at its two ends" || bad "outml layout: $(cat "$GITHUB_OUTPUT")"
eq "the plan lines (even one that looks like an output) stay inside the block" "$(sed -n '2,5p' "$GITHUB_OUTPUT" | tr '\n' '|')" "line one|ghost=1|TFPLS_deadbeef|last line|"
: > "$GITHUB_OUTPUT"; outml plan_text a; d1=$(head -1 "$GITHUB_OUTPUT"); : > "$GITHUB_OUTPUT"; outml plan_text a; d2=$(head -1 "$GITHUB_OUTPUT")
[ "$d1" != "$d2" ] && ok "the delimiter is random for every output" || bad "same delimiter twice"

# the tofu action hands the plan to the report step as an output, not as a file left on the runner
RUN="$here/actions/tofu/run.sh"
grep -q 'plan_text_out' "$RUN" && grep -q 'outml plan_text' "$RUN" && grep -q 'plan_text:' "$here/actions/tofu/action.yml" && ok "the tofu action has a plan_text output" || bad "plan_text output missing"
eq "drift.yml passes plan_text to the drift report" "$(grep -c 'plan_text: ${{ steps.t.outputs.plan_text }}' "$here/.github/workflows/drift.yml")" "1"
grep -q 'PLAN_TEXT: ${{ inputs.plan_text }}' "$here/actions/drift-report/action.yml" && ok "the drift-report action passes plan_text to report.sh" || bad "plan_text not wired in drift-report"

# never fails the run
TFPLS_REMOTE_URL="$T/missing.git" step terraform/x drift; eq "unreachable data branch: exit 0, transition unstored (the workflow alerts on any non-clean result)" "$tr_|$(grep -c '::warning::drift report' "$T/out")" "unstored|1"
BRANCH=main step terraform/x drift; eq "refuses to write to main" "$tr_|$(grep -c refusing "$T/out")" "none|1"
step terraform/x bogus; eq "unknown state is ignored with a warning" "$tr_|$(grep -c 'unknown state' "$T/out")" "none|1"

# an open issue of this workspace that was never recorded (a lost write) is reused, not duplicated
: > "$T/log"; step terraform/dup in-sync >/dev/null
STUB_EXISTING=77 STUB_EXISTING_TITLE="terraform/dup" step terraform/dup drift
eq "an unrecorded open drift issue is reused (no second issue)" "$tr_|$is_|$(n 'issue create')|$(n 'issue edit 77')" "drifted|77|0|1"

done_tests
