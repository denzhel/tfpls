#!/usr/bin/env bash
# `tfpls tofu` (the engine of actions/tofu) end to end with a stub tofu, conftest, git and gh. These are the behaviours the former
# tests read out of run.sh as text: cleanup of the workspace, least privilege of the commands that run the pull request's code,
# the target allowlist, multi-line outputs, saving or not saving a plan, the lock summary, parallelism on apply, the apply view.
# Run: bash tests/tofu-run.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin"
build_bin tfpls "$T/bin/tfpls" || { echo "FAIL go build ./cmd/tfpls"; exit 1; }

cat > "$T/bin/tofu" <<'STUB'
#!/usr/bin/env bash
echo "$*" >> "$STUB_DIR/tofu.log"
env | grep -E '^(GH_TOKEN|GITHUB_TOKEN|PLAN_ENCRYPTION_KEY|AWS_ACCESS_KEY_ID)=' | sed "s/^/tofu:$1:/" >> "$STUB_DIR/env.log"
case "$1" in
  version) echo '{"terraform_version":"1.13.1"}';;
  init) [ -z "${STUB_INIT_FAIL:-}" ] || { echo "Error: Failed to query available provider packages"; exit 1; };;
  workspace) [ "${STUB_WS_FAIL:-}" ] && exit 1; exit 0;;
  plan)
    for a in "$@"; do case "$a" in -out=*) echo plan > "${a#-out=}";; esac; done
    case "${STUB_PLAN:-update}" in
      none) echo "No changes."; exit 0;;
      fail) printf '%b' "${STUB_PLAN_TEXT:-Error: boom\n}"; exit 1;;
      kill) exit 137;;
      *) printf 'aws_sqs_queue.a: Refreshing state...\n  # aws_sqs_queue.a will be created\nPlan: 1 to add, 0 to change, 0 to destroy.\n'; exit 2;;
    esac;;
  show)
    if [ "$2" = -json ]; then echo '{"resource_changes":[{"address":"aws_sqs_queue.a","change":{"actions":["create"]}}]}'
    else printf '%b' "${STUB_SHOW:-  # aws_sqs_queue.a will be created\nPlan: 1 to add, 0 to change, 0 to destroy.\n}"; fi;;
  apply)
    [ -z "${STUB_APPLY_FAIL:-}" ] || { printf 'aws_sqs_queue.a: Creation complete after 1s [id=x]\nError: creating b: nope\n'; exit 1; }
    printf 'aws_sqs_queue.a: Refreshing state...\naws_sqs_queue.a: Creation complete after 1s\n\nApply complete! Resources: 1 added, 0 changed, 0 destroyed.\n\nOutputs:\n\ntoken = "hunter2"\n';;
esac
exit 0
STUB
cat > "$T/bin/conftest" <<'STUB'
#!/usr/bin/env bash
env | grep -E '^(GH_TOKEN|GITHUB_TOKEN|PLAN_ENCRYPTION_KEY|AWS_ACCESS_KEY_ID)=' | sed "s/^/conftest:/" >> "$STUB_DIR/env.log"
echo "conftest $*" >> "$STUB_DIR/tofu.log"
exit "${STUB_CONFTEST_RC:-0}"
STUB
cat > "$T/bin/git" <<'STUB'
#!/usr/bin/env bash
case "$*" in *rev-parse*) echo tree0123456789;; esac
STUB
cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
echo "gh $*" >> "$STUB_DIR/gh.log"
case "$*" in
  *"-f body="*) for a in "$@"; do case "$a" in body=*) printf '%s\n' "${a#body=}" > "$STUB_DIR/posted";; esac; done; echo '{"id":9,"html_url":"https://github.com/o/r/pull/1#issuecomment-9"}';;
  *comments*) echo '[]';;
  *) exit 1;;
esac
STUB
chmod +x "$T/bin/tofu" "$T/bin/conftest" "$T/bin/git" "$T/bin/gh"

# go [VAR=value ...]: one run for terraform/dev in stack/ (other directory: other/), arguments override the environment
go() {
  rm -rf "$T/repo" "$T/tmp"; mkdir -p "$T/repo/stack/.terraform" "$T/repo/other" "$T/tmp"
  echo repo > "$T/repo/stack/main.tf"; echo keep > "$T/repo/other/plan.json"; echo x > "$T/repo/stack/.terraform/p"
  : > "$T/gho"; : > "$T/summary"; : > "$T/tofu.log"; : > "$T/env.log"; : > "$T/gh.log"; rm -f "$T/posted"
  env -i PATH="$T/bin:$PATH" HOME="$T" STUB_DIR="$T" GITHUB_REPOSITORY=o/r GITHUB_REPOSITORY_ID=100 DEFAULT_BRANCH=main \
    GITHUB_OUTPUT="$T/gho" GITHUB_STEP_SUMMARY="$T/summary" RUNNER_TEMP="$T/tmp" REPO_ROOT="$T/repo" RUN_URL=https://github.com/o/r/actions/runs/5 \
    GH_TOKEN=secret1 GITHUB_TOKEN=secret2 AWS_ACCESS_KEY_ID=AKIA \
    ENTRY='{"name":"terraform/dev","label":"dev","dir":"stack","workspace":"","auto_apply":"never"}' APPLY=none "$@" \
    "$T/bin/tfpls" tofu > "$T/out" 2>&1
  rc=$?
}
out() { grep "^$1=" "$T/gho" | tail -1 | cut -d= -f2-; }

# ---- least privilege: tofu and conftest (the pull request's code) get neither the GitHub token nor the plan key
go PLAN_ENCRYPTION_KEY=secret3 SAVE_PLAN=true HEAD_SHA=abc POLICY_PACKS= POLICY_DIR=pol REPO_PRIVATE=true PLAN_MODE=fresh
mkdir -p "$T/repo/pol"
go PLAN_ENCRYPTION_KEY=secret3 POLICY_DIR=pol
eq "tofu and conftest get no GitHub token and no plan key (the cloud credentials stay)" "$(sort -u "$T/env.log" | tr '\n' ' ')" "conftest:AWS_ACCESS_KEY_ID=AKIA tofu:init:AWS_ACCESS_KEY_ID=AKIA tofu:plan:AWS_ACCESS_KEY_ID=AKIA tofu:show:AWS_ACCESS_KEY_ID=AKIA tofu:version:AWS_ACCESS_KEY_ID=AKIA "

# ---- cleanup: the working files go, the repository's files stay
go
eq "a run removes its working files and .terraform and keeps the code" "$(ls -A "$T/repo/stack" | tr '\n' ' ')" "main.tf "
eq "a run does not touch other directories" "$(cat "$T/repo/other/plan.json")" keep
go ENTRY='{"name":"terraform/dev","dir":"missing"}'
eq "a missing directory is an error, nothing is deleted" "$rc|$(out status)|$(out summary)|$(ls -A "$T/repo/stack" | tr '\n' ' ')" "1|error|dir not found: missing|.terraform main.tf "
go ENTRY='{"name":"terraform/dev","dir":"../.."}'
eq "a directory outside the workspace is refused" "$rc|$(out summary)" "1|dir not found: ../.."
go ENTRY='not json'
eq "an entry that is not JSON fails loudly" "$rc|$(out status)" "1|error"

# ---- target allowlist
go TARGETS='aws_sqs_queue.a;rm'
eq "an invalid -target is refused before tofu runs" "$rc|$(out status)|$(out summary)|$(cat "$T/tofu.log" | wc -l | tr -d ' ')" '1|error|Invalid -target value: `aws_sqs_queue.a;rm`|0'
go TARGETS='module.a aws_x.b["k"]'
eq "valid targets reach the plan as separate -target arguments" "$(grep -c -- '^plan .*-target=module.a -target=aws_x.b\["k"\]$' "$T/tofu.log")" 1

# ---- plan results and outputs
go
eq "a plan with changes: status planned, counts and changes outputs" "$rc|$(out status)|$(out counts)|$(out changes)|$(out summary)" '0|planned|{"add":1,"change":0,"destroy":0,"replace":0}|[{"action":"added","address":"aws_sqs_queue.a"}]|+1 ~0 -0'
d=$(grep '^plan_text<<' "$T/gho" | head -1 | sed 's/^plan_text<<//')
[ -n "$d" ] && [ "$(grep -cF "$d" "$T/gho")" = 2 ] && ok "the plan text is a multi-line output with a delimiter that occurs only at its two ends" || bad "plan_text block: $(cat "$T/gho")"
go STUB_SHOW=$'line one\nghost=1\nTFPLS_deadbeef\nPlan: 1 to add, 0 to change, 0 to destroy.\n'
d=$(grep '^plan_text<<' "$T/gho" | head -1 | sed 's/^plan_text<<//'); first=$(grep -n '^plan_text<<' "$T/gho" | cut -d: -f1)
eq "a plan line that looks like an output stays inside the block" "$(sed -n "$((first+1)),$((first+3))p" "$T/gho" | tr '\n' '|')" "line one|ghost=1|TFPLS_deadbeef|"
go STUB_PLAN=none
eq "no changes: status no-changes and no plan_text" "$rc|$(out status)|$(grep -c '^plan_text' "$T/gho")" "0|no-changes|0"
go STUB_PLAN=kill
eq "a plan killed by a signal is a failed plan, not a plan with changes (the Bash treated every code but 0 and 1 as changes)" "$rc|$(out status)|$(out summary)" "1|error|plan failed"
go STUB_PLAN=fail STUB_PLAN_TEXT='╷\n│ Error: Error acquiring the state lock\n│ \n│ Lock Info:\n│   ID:        55555555-aaaa-bbbb-cccc-000000000005\n│   Operation: OperationTypeApply\n│   Who:       crashed@runner\n│   Created:   2026-10-09 10:00:00.123 +0000 UTC\n╵\n'
eq "a failed plan on a held lock names who holds it and since when" "$(out summary)" "plan failed: the state is locked by crashed@runner since 2026-10-09 10:00:00 (OperationTypeApply)"
go STUB_INIT_FAIL=1
eq "a failed init is an error" "$rc|$(out summary)" "1|init failed"

# ---- saving the plan
go PR=1 HEAD_SHA=abc SAVE_PLAN=true REPO_PRIVATE=false
eq "a public repository without a key never saves a plan" "$(out plan_path)|$(grep -c 'Plan not saved' "$T/posted")|$(grep -c '^`/tofu apply dev` to apply' "$T/posted")" "|1|0"
go PR=1 HEAD_SHA=abc SAVE_PLAN=true REPO_PRIVATE=
eq "an unknown visibility is treated as public" "$(out plan_path)" ""
go PR=1 HEAD_SHA=abc SAVE_PLAN=true REPO_PRIVATE=true
case "$(out plan_path)" in */plan.tgz) ok "a private repository without a key saves an unencrypted plan";; *) bad "plan_path '$(out plan_path)'";; esac
eq "... and says so, with the plan name" "$(grep -c 'is not set: the saved plan is stored unencrypted' "$T/posted")|$(out plan_name)" "1|tfpls-plan-terraform__dev-abc-all"
go PR=1 HEAD_SHA=abc SAVE_PLAN=true REPO_PRIVATE=true ALLOW_UNENCRYPTED_PLANS=true
eq "allow_unencrypted_plans silences the warning only" "$(grep -c 'stored unencrypted' "$T/posted")" 0
go PR=1 HEAD_SHA=abc SAVE_PLAN=true REPO_PRIVATE=false ALLOW_UNENCRYPTED_PLANS=true
eq "allow_unencrypted_plans does not let a public repository save a plan" "$(out plan_path)" ""
go PR=1 HEAD_SHA=abc SAVE_PLAN=true REPO_PRIVATE=false PLAN_ENCRYPTION_KEY=k
case "$(out plan_path)" in */plan.tgz.enc) ok "with a key even a public repository saves an encrypted plan";; *) bad "plan_path '$(out plan_path)'";; esac
go PR=1 SAVE_PLAN=true REPO_PRIVATE=true
eq "no head SHA: the plan is not saved" "$(out plan_path)|$(grep -c 'no head SHA' "$T/out")" "|1"

# ---- apply
go APPLY=force PR=1
eq "apply gets -parallelism and -lock-timeout" "$(grep -c -- '^apply -input=false -no-color -parallelism=10 -lock-timeout=0s tfplan$' "$T/tofu.log")" 1
eq "a successful apply: status applied, the comment shows the apply and never the Outputs block" "$(out status)|$(grep -c 'Apply complete' "$T/posted")|$(grep -c 'hunter2\|Outputs:' "$T/posted")|$(grep -c 'Refreshing state' "$T/posted")" "applied|1|0|0"
go APPLY=force PR=1 STUB_APPLY_FAIL=1 GITHUB_SHA=0123456789abcdef
eq "a failed apply: error with the rollback hints" "$rc|$(out status)|$(grep -c 'The apply failed part-way' "$T/posted")|$(grep -c 'git revert 0123456789ab' "$T/posted")" "1|error|1|1"
go APPLY=auto AUTO_APPLY=additive_only
eq "additive_only applies a plan that only creates" "$(out status)" applied
go APPLY=auto AUTO_APPLY=never
eq "auto_apply never does not apply: skipped, with the reason" "$(out status)|$(grep -c '^apply' "$T/tofu.log")" "skipped|0"
go PARALLELISM=3 LOCK_TIMEOUT=2m APPLY=force
eq "settings reach the apply" "$(grep -c -- '^apply.*-parallelism=3 -lock-timeout=2m' "$T/tofu.log")" 1

# ---- policy
go POLICY_PACKS='../x'
eq "an unsafe policy pack name is refused" "$rc|$(out status)" "1|error"
go POLICY_DIR=pol STUB_CONFTEST_RC=1
eq "a failing policy stops the run" "$rc|$(out status)" "1|policy-failed"

done_tests
