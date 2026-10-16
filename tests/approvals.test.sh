#!/usr/bin/env bash
# Tests for actions/approvals: check.sh (modes), reviews.sh (who counts) and enforce.sh (per-workspace policy)
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
A="$here/actions/approvals"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
rev() { jq -cn --arg u "$1" --arg s "$2" --arg c "$3" --arg t "${4:-2026-10-10T10:00:00Z}" '{user:{login:$u,type:"User"},state:$s,commit_id:$c,submitted_at:$t}'; }
mk() { printf '%s\n' "$@" | jq -s '.' > "$T/r.json"; }
chk() { bash "$A/check.sh" "$T/r.json" HEAD "$1" "$2"; echo "rc=$?"; }

# ---- check.sh
mk "$(rev a APPROVED HEAD)"
eq "strict: an approval of the current commit" "$(chk strict 1 | tr '\n' ' ')" "ok rc=0 "
eq "basic: the same" "$(chk basic 1 | tr '\n' ' ')" "ok rc=0 "
mk "$(rev a APPROVED OLD)"
eq "strict: an approval of an older commit is refused and says why" "$(chk strict 1 | tr '\n' '|')" "the approval is for an older commit: new commits were pushed after the review. Ask for a new review of HEAD|rc=1|"
eq "basic: an approval of an older commit still counts" "$(chk basic 1 | tr '\n' ' ')" "ok rc=0 "
mk
eq "strict: no review at all" "$(chk strict 1 | head -1)" "apply needs 1 approving review(s) of the current commit (HEAD); found 0"
eq "basic: no review at all" "$(chk basic 1 | head -1)" "apply needs 1 approving review(s); found 0"
mk "$(rev a APPROVED HEAD 2026-10-10T10:00:00Z)" "$(rev a APPROVED HEAD 2026-10-10T11:00:00Z)"
eq "the same reviewer twice counts once" "$(chk basic 2 | head -1)" "apply needs 2 approving review(s); found 1"
mk "$(rev a APPROVED HEAD)" "$(rev b APPROVED OLD)"
eq "strict: 2 required, one of them is old" "$(chk strict 2 | tail -1)" "rc=1"
eq "basic: 2 required, both count" "$(chk basic 2 | tail -1)" "rc=0"
eq "basic: 3 required, only 2" "$(chk basic 3 | head -1)" "apply needs 3 approving review(s); found 2"
mk "$(rev a APPROVED HEAD 2026-10-10T10:00:00Z)" "$(rev a CHANGES_REQUESTED HEAD 2026-10-10T11:00:00Z)"
eq "a later change request cancels the approval in both modes" "$(chk strict 1 | tail -1)|$(chk basic 1 | tail -1)" "rc=1|rc=1"
mk "$(rev a CHANGES_REQUESTED HEAD 2026-10-10T10:00:00Z)" "$(rev a APPROVED HEAD 2026-10-10T11:00:00Z)"
eq "a later approval replaces the change request" "$(chk strict 1 | tail -1)|$(chk basic 1 | tail -1)" "rc=0|rc=0"
mk "$(rev a APPROVED HEAD)" "$(rev b CHANGES_REQUESTED HEAD)"
eq "someone else's outstanding change request blocks" "$(chk basic 1 | head -1)" "a reviewer requested changes; apply needs 1 approving review(s)"
mk "$(rev a APPROVED HEAD 2026-10-10T10:00:00Z)" "$(rev a DISMISSED HEAD 2026-10-10T11:00:00Z)"
eq "a dismissed review does not count" "$(chk basic 1 | tail -1)" "rc=1"
mk "$(rev a COMMENTED HEAD)"
eq "a comment is not an approval" "$(chk basic 1 | tail -1)" "rc=1"
mk
eq "zero required approvals is always satisfied" "$(chk strict 0 | tr '\n' ' ')" "ok rc=0 "
eq "bad usage exits 2" "$(bash "$A/check.sh" "$T/r.json" HEAD sloppy 1 >/dev/null 2>&1; echo $?)" 2

# ---- enforce.sh: per-workspace policy with defaults
mk "$(rev a APPROVED OLD)"
# gh stub: collaborator permission lookups answer from STUB_PERMS ("login:perm ..."; default write; "fail" = the call fails); calls are logged
mkdir -p "$T/bin"; cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
case "$*" in
  *collaborators/*/permission*)
    all="$*"; login=${all#*collaborators/}; login=${login%%/*}
    echo "$login" >> "${STUB_LOG:-/dev/null}"
    for p in ${STUB_PERMS:-}; do
      if [ "${p%%:*}" = "$login" ]; then v=${p#*:}; [ "$v" = fail ] && exit 1; echo "$v"; exit 0; fi
    done
    echo write; exit 0;;
esac
exit 0
STUB
chmod +x "$T/bin/gh"
enf() { # entries-json [env...]
  local entries=$1; shift
  env -i PATH="$T/bin:$PATH" TFPLS_DRY=1 TFPLS_REVIEWS_FILE="$T/r.json" ENTRIES="$entries" SHA=HEAD REPO=o/r PR=1 BASE_BRANCH=main \
      "$@" bash "$A/enforce.sh" 2>&1; echo "rc=$?"
}
e1='[{"name":"t/dev","approvals_mode":"","approvals_required":""}]'
eq "defaults: strict, 1 required, old approval -> refused" "$(enf "$e1" | tail -1)" "rc=1"
eq "the refusal names the mode and the source of the count" "$(enf "$e1" | grep -F '(mode `strict`, 1 required)' >/dev/null && echo yes)" yes
mk "$(rev a APPROVED OLD)"
e2='[{"name":"t/dev","approvals_mode":"basic","approvals_required":""},{"name":"t/prod","approvals_mode":"strict","approvals_required":""}]'
eq "a workspace's own mode overrides the default; with strict prod in the command the strictest wins" "$(enf "$e2" | tail -1)|$(enf "$e2" | grep -F '`t/prod`' >/dev/null && echo named)" "rc=1|named"
e3='[{"name":"t/dev","approvals_mode":"","approvals_required":"0"},{"name":"t/prod","approvals_mode":"basic","approvals_required":""}]'
eq "required 0 on one workspace, basic on the other (old approval passes basic): ok" "$(enf "$e3" | tail -1)" "rc=0"
e4='[{"name":"t/dev","approvals_mode":"basic","approvals_required":"2"}]'
eq "required 2 with one approval is refused and says so" "$(enf "$e4" | tail -1)|$(enf "$e4" | grep -F 'found 1' >/dev/null && echo found1)" "rc=1|found1"

# ---- only reviews of people with write access count
e6='[{"name":"t/dev","approvals_mode":"basic","approvals_required":""}]'
mk "$(rev bob APPROVED HEAD)"
o=$(enf "$e6" STUB_PERMS="bob:read"); eq "a read-only approver alone is refused and named" "$(tail -1 <<<"$o")|$(grep -c 'bob (no write access)' <<<"$o")" "rc=1|1"
mk "$(rev bob APPROVED HEAD)" "$(rev ann APPROVED HEAD)"
eq "a writer plus a reader passes (required 1)" "$(enf "$e6" STUB_PERMS="bob:read ann:write" | tail -1)" "rc=0"
e7='[{"name":"t/dev","approvals_mode":"basic","approvals_required":"2"}]'
o=$(enf "$e7" STUB_PERMS="bob:read ann:write"); eq "required 2 with one writer and one reader: found 1" "$(tail -1 <<<"$o")|$(grep -q 'found 1' <<<"$o" && echo yes)" "rc=1|yes"
eq "admin and write count, read does not" "$(enf "$e7" STUB_PERMS="bob:admin ann:write" | tail -1)|$(enf "$e7" STUB_PERMS="bob:none ann:write" | tail -1)" "rc=0|rc=1"
mk "$(rev ann APPROVED HEAD)" "$(rev bob CHANGES_REQUESTED HEAD)"
eq "a reader's change request does not block" "$(enf "$e6" STUB_PERMS="bob:read ann:write" | tail -1)|$(enf "$e6" STUB_PERMS="bob:write ann:write" | tail -1)" "rc=0|rc=1"
mk "$(rev bob APPROVED HEAD)"
o=$(enf "$e6" STUB_PERMS="bob:fail"); eq "a failed permission lookup does not count" "$(tail -1 <<<"$o")|$(grep -c 'permission could not be checked' <<<"$o")" "rc=1|1"
mk "$(rev bob APPROVED HEAD 2026-10-10T10:00:00Z)" "$(rev bob APPROVED HEAD 2026-10-10T11:00:00Z)" "$(rev ann APPROVED HEAD)"
: > "$T/calls"; enf "$e6" STUB_LOG="$T/calls" >/dev/null; eq "three reviews by two people make two lookups" "$(wc -l < "$T/calls" | tr -d ' ')" 2
jq -cn '{user:{login:"github-actions[bot]",type:"Bot"},state:"APPROVED",commit_id:"HEAD",submitted_at:"2026-10-10T10:00:00Z"}' > "$T/bot.json"; jq -cn '{user:null,state:"APPROVED",commit_id:"HEAD",submitted_at:"2026-10-10T10:00:00Z"}' > "$T/ghost.json"
printf '%s\n' "$(cat "$T/bot.json")" "$(cat "$T/ghost.json")" | jq -s '.' > "$T/r.json"; : > "$T/calls"
eq "a bot and a deleted user never count and are not looked up" "$(enf "$e6" STUB_LOG="$T/calls" | tail -1)|$(wc -l < "$T/calls" | tr -d ' ')" "rc=1|0"
mk "$(rev a_b APPROVED HEAD)"; : > "$T/calls"; enf "$e6" STUB_LOG="$T/calls" >/dev/null
eq "a login with an underscore is looked up" "$(cat "$T/calls")" "a_b"
mk "$(rev bob APPROVED HEAD)"; : > "$T/calls"
eq "required 0 or enabled false: nobody is looked up" "$(enf '[{"name":"t/dev","approvals_mode":"","approvals_required":"0"}]' STUB_LOG="$T/calls" | tail -1)|$(wc -l < "$T/calls" | tr -d ' ')" "rc=0|0"
o=$(enf "$e6" STUB_PERMS="bob:read"); eq "the refusal points to the ruleset and --now" "$(grep -c -e '--now' <<<"$o")" 1

# the repository does not let the pusher of the latest commit approve it
mk "$(rev bob APPROVED HEAD)"
eq "an approval by the pusher of the latest commit counts when the repository allows it" "$(enf "$e1" STUB_PERMS="bob:write" | tail -1)" "rc=0"
o=$(enf "$e1" STUB_PERMS="bob:write" LAST_PUSH=true TFPLS_COMMIT_LOGINS=bob); eq "...and does not when the repository requires approval of the most recent push by somebody else" "$(tail -1 <<<"$o")|$(grep -c 'pushed the latest commit' <<<"$o")" "rc=1|1"
mk "$(rev bob APPROVED HEAD)" "$(rev carol APPROVED HEAD)"
eq "another approver still counts" "$(enf "$e1" STUB_PERMS="bob:write,carol:write" LAST_PUSH=true TFPLS_COMMIT_LOGINS=bob | tail -1)" "rc=0"

for bad_e in '' 'not json' '[]' '{}'; do eq "no usable rules ('$bad_e') refuse instead of passing" "$(enf "$bad_e" | tail -1)" "rc=1"; done

done_tests
