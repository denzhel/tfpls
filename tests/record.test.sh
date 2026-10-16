#!/usr/bin/env bash
# Tests for actions/record/record.sh against a local bare repository standing in for GitHub.
set -uo pipefail
export TFPLS_RETRY_SLEEP=0   # retries pause randomly in the scripts under test; the tests do not wait
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
R="$here/actions/record/record.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
git init -q --bare "$T/remote.git"
export TFPLS_REMOTE_URL="$T/remote.git"
B=(WORKSPACE=dev KIND=apply STATUS=applied REPO=o/r ACTOR=alice TRIGGER=push SHA=abc RUN_URL=http://run/1 BRANCH=tfpls-data TFPLS_NOW=2026-10-10T10:00:00Z)
show() { git --git-dir="$T/remote.git" show "tfpls-data:$1" 2>/dev/null; }

env_run() { env "${B[@]}" "$@" bash "$R" >"$T/out" 2>&1; echo $?; }

rc=$(env_run COUNTS='{"add":1,"change":2,"destroy":0,"replace":0}' REVIEWED=true PR=7 SUMMARY=$'line one\nline two')
eq "first event exits 0" "$rc" 0
eq "creates the data branch with the month file" "$(show audit/2026-10.jsonl | wc -l | tr -d ' ')" 1
ev=$(show audit/2026-10.jsonl | head -1)
eq "event fields" "$(jq -r '[.workspace,.kind,.status,.actor,.pr,.reviewed,.counts.change,.run]|map(tostring)|join("|")' <<<"$ev")" "dev|apply|applied|alice|7|true|2|http://run/1"
eq "newlines in the summary are flattened" "$(jq -r .summary <<<"$ev")" "line one line two"
eq "README explains the branch" "$(show README.md | head -1)" "# tfpls data"

rc=$(env_run WORKSPACE=prod KIND=unlock STATUS=unlocked TFPLS_NOW=2026-10-11T10:00:00Z)
eq "second event appends" "$(show audit/2026-10.jsonl | wc -l | tr -d ' ')" 2
eq "no PR/reviewed/counts become null" "$(show audit/2026-10.jsonl | tail -1 | jq -c '[.pr,.reviewed,.counts]')" "[null,null,null]"
rc=$(env_run TFPLS_NOW=2026-11-01T00:00:00Z)
eq "a new month starts a new file" "$(show audit/2026-11.jsonl | wc -l | tr -d ' ')" 1
eq "every line is valid JSON" "$(show audit/2026-10.jsonl | jq -c . | wc -l | tr -d ' ')" 2

# parallel writers must not lose events
for i in 1 2 3 4 5 6; do ( env_run WORKSPACE="ws$i" TFPLS_NOW=2026-12-01T00:00:0${i}Z TFPLS_RETRY_SLEEP="$(awk "BEGIN{print $i * 0.2}")" >/dev/null ) & done; wait
eq "6 parallel writers all land" "$(show audit/2026-12.jsonl | jq -r .workspace | sort | tr '\n' ,)" "ws1,ws2,ws3,ws4,ws5,ws6,"

# never fails the job
rc=$(env_run TFPLS_REMOTE_URL="$T/missing.git")
eq "unreachable remote: exit 0 with a warning" "$rc|$(grep -c '::warning::audit log' "$T/out")" "0|1"
rc=$(env_run BRANCH=main); eq "refuses to write to main" "$rc|$(grep -c 'refusing' "$T/out")" "0|1"
rc=$(env_run BRANCH='a b'); eq "rejects odd branch names" "$rc|$(grep -c 'invalid branch' "$T/out")" "0|1"
rc=$(env_run COUNTS='not json'); eq "bad counts become null" "$rc|$(show audit/2026-10.jsonl | tail -1 | jq -c .counts)" "0|null"


rc=$(env_run DURATION=41 TFPLS_NOW=2026-12-04T00:00:00Z); eq "duration is stored in seconds" "$(show audit/2026-12.jsonl | tail -1 | jq -c '.duration_s')" "41"

rc=$(env_run LOCK_ID=11111111-aaaa VIA=lock-sweep TFPLS_NOW=2026-12-05T00:00:00Z); eq "lock id and trigger are stored" "$(show audit/2026-12.jsonl | tail -1 | jq -c '[.lock_id,.via]')" '["11111111-aaaa","lock-sweep"]'

done_tests
