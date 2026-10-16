#!/usr/bin/env bash
# Tests for actions/codeowners/check.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
C="$here/actions/codeowners/check.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
cat > "$T/CODEOWNERS" <<'X'
# default owners
*                              @platform
/workspaces/prod-*/            @alice @org/sre
/workspaces/dr-us-east-1/      @bob
/workspaces/dev-us-east-1/     @carol   # trailing comment
docs/                          @writer
X
printf '@org/sre dave erin\n' > "$T/teams"; export TFPLS_TEAMS_FILE="$T/teams"
rev() { # login state sha [time]
  jq -cn --arg u "$1" --arg s "$2" --arg c "$3" --arg t "${4:-2026-10-10T10:00:00Z}" '{user:{login:$u},state:$s,commit_id:$c,submitted_at:$t}'; }
mk() { printf '%s\n' "$@" | jq -s '.' > "$T/reviews.json"; }
c() { bash "$C" "$T/CODEOWNERS" "$T/reviews.json" HEAD "$@" 2>&1; echo "rc=$?"; }

mk "$(rev alice APPROVED HEAD)"
eq "a user owner approving satisfies the path" "$(c workspaces/prod-us-west-1 | tail -1)" "rc=0"
mk "$(rev dave APPROVED HEAD)"
eq "a member of an owning team satisfies it" "$(c workspaces/prod-us-west-1 | tail -1)" "rc=0"
mk "$(rev mallory APPROVED HEAD)"
eq "someone else's approval does not" "$(c workspaces/prod-us-west-1 | tail -1)|$(c workspaces/prod-us-west-1 | grep -c '@alice @org/sre')" "rc=1|1"
mk "$(rev alice APPROVED OLD)"
eq "an approval of an older commit does not count" "$(c workspaces/prod-us-west-1 | tail -1)" "rc=1"
mk "$(rev alice APPROVED HEAD 2026-10-10T10:00:00Z)" "$(rev alice CHANGES_REQUESTED HEAD 2026-10-10T11:00:00Z)"
eq "a later change request from the owner cancels the approval" "$(c workspaces/prod-us-west-1 | tail -1)" "rc=1"
mk "$(rev ALICE APPROVED HEAD)"
eq "logins are compared case-insensitively" "$(c workspaces/prod-us-west-1 | tail -1)" "rc=0"

mk "$(rev alice APPROVED HEAD)"
eq "every path must be satisfied" "$(c workspaces/prod-us-west-1 workspaces/dr-us-east-1 | tail -1)" "rc=1"
mk "$(rev alice APPROVED HEAD)" "$(rev bob APPROVED HEAD)"
eq "all owners present: ok" "$(c workspaces/prod-us-west-1 workspaces/dr-us-east-1 | tail -1)" "rc=0"
eq "the last matching rule wins (dr has its own owner, not the prod glob)" "$(mk "$(rev alice APPROVED HEAD)"; c workspaces/dr-us-east-1 | tail -1)" "rc=1"
mk "$(rev carol APPROVED HEAD)"
eq "trailing comments in a rule are ignored" "$(c workspaces/dev-us-east-1 | tail -1)" "rc=0"
mk "$(rev platform APPROVED HEAD)"
eq "the catch-all rule applies to unlisted paths" "$(c workspaces/stg-us-west-2 | tail -1)" "rc=0"
mk "$(rev nobody COMMENTED HEAD)"
eq "a comment is not an approval" "$(c workspaces/stg-us-west-2 | tail -1)" "rc=1"
: > "$T/empty"; mk "$(rev x APPROVED HEAD)"
eq "no CODEOWNERS match: no requirement" "$(bash "$C" "$T/empty" "$T/reviews.json" HEAD workspaces/x >/dev/null 2>&1; echo $?)" "0"
eq "usage errors exit 2" "$(bash "$C" "$T/nope" "$T/reviews.json" HEAD p >/dev/null 2>&1; echo $?)" "2"

# enforce.sh: only a 404 means "no CODEOWNERS file"; an API error must refuse
mkdir -p "$T/gb"; printf '#!/bin/sh\ncase "$*" in *contents*) echo "$FAKE_ERR" >&2; exit 1;; *) exit 0;; esac\n' > "$T/gb/gh"; chmod +x "$T/gb/gh"
run_enf() { env -i PATH="$T/gb:$PATH" FAKE_ERR="$1" REPO=o/r BASE=main PR=1 SHA=abc ENTRIES='[]' bash "$here/actions/codeowners/enforce.sh" >/dev/null 2>&1; echo $?; }
eq "a missing CODEOWNERS file (404) enforces nothing" "$(run_enf 'gh: Not Found (HTTP 404)')" 0
eq "an API error refuses" "$(run_enf 'gh: Bad Gateway (HTTP 502)')" 1

done_tests
