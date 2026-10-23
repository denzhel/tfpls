#!/usr/bin/env bash
# actions/drift-fix: `tfpls drift-fix parse` (commands, guards) and `filter` (drift_fix: false) with a gh stub (no network).
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT; mkdir -p "$T/bin"
(cd "$here" && go build -o "$T/tfpls" ./cmd/tfpls) || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
args="$*"
echo "$args" >> "$STUB_LOG"
case "$args" in
  *"/permission"*) echo "${STUB_PERM:-write}";;
  *"/commits/"*) echo "abc123def456";;
  *"issue comment"*) shift; ;;
esac
exit 0
STUB
chmod +x "$T/bin/gh"

run() { # env assignments...  -> rc, outputs in $T/out, calls in $T/log
  : > "$T/out"; : > "$T/log"
  env -i PATH="$T/bin:$PATH" STUB_LOG="$T/log" GITHUB_OUTPUT="$T/out" REPO=o/r DEFAULT_BRANCH=main ACTOR=alice COMMENT_ID=7 \
    EVENT=issue_comment ISSUE_NUMBER=9 ISSUE_TITLE="Drift: dev-eu-north-1" ISSUE_STATE=open IS_PR=false ISSUE_USER_TYPE=Bot ISSUE_LABELS='["tfpls-drift"]' "$@" "$T/tfpls" drift-fix parse >"$T/stdout" 2>&1
  rc=$?
}
o() { sed -n "s/^$1=//p" "$T/out" | tail -1; }

run BODY="/tofu plan"
eq "plan on a drift issue" "$rc|$(o ok)|$(o cmd)|$(o names)|$(o sha)|$(o issue)" "0|true|plan|dev-eu-north-1|abc123def456|9"
run BODY="/tofu apply"; eq "apply" "$rc|$(o cmd)" "0|apply"
run BODY="/tofu fix"; eq "fix" "$rc|$(o cmd)" "0|fix"
run BODY="/tofu fix -target=aws_instance.x -target=module.vpc.aws_nat_gateway.this[0]"
eq "targets are collected" "$rc|$(o targets)" "0|aws_instance.x module.vpc.aws_nat_gateway.this[0]"
run BODY="/tofu fix prod-eu-north-1"; eq "a workspace argument is refused (the issue is one workspace)" "$rc|$(o ok)|$(grep -c 'unexpected argument' "$T/log")" "1|false|1"
run BODY="/tofu fix -target=\$(rm -rf)"; eq "an unsafe target is refused" "$rc|$(o ok)" "1|false"
run BODY="/tofu unlock"; eq "an unknown command is refused with the reason on the issue" "$rc|$(grep -c 'unknown command' "$T/log")" "1|1"
run BODY="/tofu apply" ISSUE_USER_TYPE=User; eq "an issue opened by a person is not a drift issue" "$rc|$(o ok)|$(grep -c 'not a drift issue made by the pipeline' "$T/log")" "0|false|1"
run BODY="/tofu apply" ISSUE_LABELS='[]'; eq "an issue without the drift label is not a drift issue" "$rc|$(o ok)" "0|false"
run BODY=$'/tofu plan\r'; eq "a CRLF comment is understood" "$rc|$(o cmd)" "0|plan"
mkdir -p "$T/glob"; touch "$T/glob/aaa"; run BODY="/tofu fix -target=*" ; eq "a glob is never expanded against the directory" "$(grep -c aaa "$T/stdout")" 0
run BODY="/tofu plan" STUB_PERM=read; eq "a reader is refused" "$rc|$(o ok)|$(grep -c 'lacks write access' "$T/log")" "1|false|1"
run BODY="/tofu plan" STUB_PERM=none; eq "a stranger is refused" "$rc|$(o ok)" "1|false"
run BODY="/tofu plan" ISSUE_TITLE="Fix the docs"; eq "an unrelated issue is told where the commands work and ignored (no red run)" "$rc|$(o ok)|$(grep -c 'Drift: &lt;workspace' "$T/log")" "0|false|1"
run BODY="/tofu plan" ISSUE_STATE=closed; eq "a closed drift issue is ignored" "$rc|$(o ok)" "0|false"
run BODY="/tofu plan" IS_PR=true; eq "a pull request is not handled here" "$rc|$(o ok)|$(wc -l < "$T/log" | tr -d ' ')" "0|false|0"
run BODY="/tofu plan" ISSUE_TITLE="Drift: stack/dev; rm"; eq "a title that is not a plain workspace name is ignored" "$rc|$(o ok)" "0|false"

# manual run
run EVENT=workflow_dispatch ISSUE_NUMBER= ISSUE_TITLE= ISSUE_STATE= BODY= IN_WORKSPACES="dev-eu-north-1 stg-eu-north-1" IN_STEP=plan
eq "manual: names and step" "$rc|$(o cmd)|$(o names)|$(o issue)" "0|plan|dev-eu-north-1 stg-eu-north-1|"
run EVENT=workflow_dispatch ISSUE_NUMBER= BODY= IN_WORKSPACES="dev-eu-north-1,stg-eu-north-1" IN_STEP=
eq "manual: comma separated names, the step defaults to fix" "$rc|$(o cmd)|$(o names)" "0|fix|dev-eu-north-1 stg-eu-north-1"
run EVENT=workflow_dispatch ISSUE_NUMBER= BODY= IN_WORKSPACES="prod-*" IN_STEP=fix; eq "manual: a glob is refused" "$rc|$(o ok)" "1|false"
run EVENT=workflow_dispatch ISSUE_NUMBER= BODY= IN_WORKSPACES="" IN_STEP=fix; eq "manual: no workspace is refused" "$rc|$(o ok)" "1|false"
run EVENT=workflow_dispatch ISSUE_NUMBER= BODY= IN_WORKSPACES="dev" IN_STEP=destroy; eq "manual: an unknown step is refused" "$rc|$(o ok)" "1|false"

# filter
E='{"dev":{"label":"dev","drift_fix":"true"},"prod":{"label":"prod","drift_fix":"false"}}'
: > "$T/out"; : > "$T/log"; env -i PATH="$T/bin:$PATH" STUB_LOG="$T/log" GITHUB_OUTPUT="$T/out" REPO=o/r ISSUE_NUMBER=9 ENTRIES="$E" "$T/tfpls" drift-fix filter >/dev/null 2>&1
eq "filter: a workspace with drift_fix: false is dropped and the issue says why" "$(o any)|$(o matrix)|$(grep -c 'drift_fix: false' "$T/log")" 'true|{"include":[{"label":"dev"}]}|1'
: > "$T/out"; env -i PATH="$T/bin:$PATH" STUB_LOG="$T/log" GITHUB_OUTPUT="$T/out" REPO=o/r ENTRIES='{"prod":{"label":"prod","drift_fix":"false"}}' "$T/tfpls" drift-fix filter >/dev/null 2>&1
eq "filter: nothing left means nothing runs" "$(o any)" false

done_tests
