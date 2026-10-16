#!/usr/bin/env bash
# Tests for actions/unlock/unlock.sh in sweep mode with stubbed tofu and gh (no network, no state backend).
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
U="$here/actions/unlock/unlock.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin" "$T/repo/dir"

# tofu stub: `plan` fails with a lock error when LOCK_CREATED is set; force-unlock records the call
cat > "$T/bin/tofu" <<'STUB'
#!/usr/bin/env bash
case "$1" in
  init) exit 0;;
  workspace) [ -n "${WS_MISSING:-}" ] && [ "${2:-}" = select ] && exit 1; exit 0;;
  state) [ -n "${PROBE_FAIL:-}" ] && { echo "Error: something else went wrong"; exit 1; }
        [ -n "${NO_STATE:-}" ] && { printf 'Error: No state file was found!\n'; exit 1; }
        if [ -n "${LOCK_CREATED:-}" ]; then
          printf 'Error: Error acquiring the state lock\n\nLock Info:\n  ID:        a1b2c3d4-e5f6-7890-abcd-ef1234567890\n  Path:      s/x\n  Operation: OperationTypeApply\n  Who:       ci@host\n  Version:   1.10.6\n  Created:   %s\n' "$LOCK_CREATED"; exit 1
        else printf 'Error: Invalid source address\n\nCannot move tfpls_probe.a: it does not exist.\n'; exit 1; fi;;
  force-unlock) echo "$*" >> "$STUB_LOG"; exit 0;;
esac
STUB
# gh stub: lists runs from STUB_RUNS, and jobs from STUB_JOBS
cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
args="$*"
echo "$args" >> "${GH_LOG:-/dev/null}"
case "$args" in
  *"actions/runs?status="*) for r in ${STUB_RUNS:-}; do echo "$r"; done;;
  *"/jobs"*) printf '%s' "${STUB_JOBS:-{\"jobs\":[]\}}";;
esac
exit 0
STUB
chmod +x "$T/bin/"*

run() { # created sweep -> sets rc, result, detail
  : > "$T/log"; : > "$T/gho"
  LOCK_CREATED="$1" REPORT_ONLY="${2:-${REPORT_ONLY:-false}}" PATH="$T/bin:$PATH" STUB_LOG="$T/log" GITHUB_OUTPUT="$T/gho" \
    NAME=ws LABEL=ws DIR=dir WS="${WS:-}" REPO_ROOT="$T/repo" RUN_URL=u GITHUB_REPOSITORY=o/r GITHUB_RUN_ID=999 bash "$U" >"$T/out" 2>&1
  rc=$?; result=$(sed -n 's/^result=//p' "$T/gho" | tail -1); detail=$(sed -n 's/^detail=//p' "$T/gho" | tail -1)
}
ago() { date -u -v-"$1"M '+%Y-%m-%d %H:%M:%S.000 +0000 UTC' 2>/dev/null || date -u -d "$1 minutes ago" '+%Y-%m-%d %H:%M:%S.000 +0000 UTC'; }

run "$(ago 90)"; eq "a 90 minute old lock with no active run is released" "$rc|$result|$(grep -c force-unlock "$T/log")" "0|unlocked|1"
eq "the released lock id is passed to force-unlock" "$(cat "$T/log")" "force-unlock -force a1b2c3d4-e5f6-7890-abcd-ef1234567890"
run "$(ago 2)"; eq "a lock younger than 5 minutes is kept" "$rc|$result|$(grep -c force-unlock "$T/log")" "1|refused|0"
run ""; eq "no lock: nothing to do" "$rc|$result|$(grep -c force-unlock "$T/log")" "0|not-locked|0"
STUB_RUNS=5 STUB_JOBS='{"jobs":[{"name":"apply / ws","status":"in_progress","html_url":"http://job/1"}]}' run "$(ago 90)"
eq "an active run on the workspace keeps the lock" "$rc|$result|$(grep -c force-unlock "$T/log")" "1|refused|0"
STUB_RUNS=5 STUB_JOBS='{"jobs":[{"name":"apply / other","status":"in_progress","html_url":"http://job/1"}]}' run "$(ago 90)"
eq "an active run on another workspace does not" "$rc|$result" "0|unlocked"
STUB_RUNS=5 STUB_JOBS='{"jobs":[{"name":"tofu / apply / apply (ws)","status":"in_progress","html_url":"http://job/1"}]}' run "$(ago 90)"
eq "the default matrix job name 'apply (ws)' counts as an active run on the workspace" "$rc|$result|$(grep -c force-unlock "$T/log")" "1|refused|0"
STUB_RUNS=5 STUB_JOBS='{"jobs":[{"name":"tofu / apply / apply (other-ws)","status":"in_progress","html_url":"http://job/1"}]}' run "$(ago 90)"
eq "'apply (other-ws)' does not" "$rc|$result" "0|unlocked"
STUB_RUNS=5 STUB_JOBS='{"jobs":[{"name":"apply / ws","status":"in_progress","html_url":"http://job/1"}]}' run "$(ago 90)"
eq "every line of the step output file is a single name=value (the multi-line active-run message is flattened)" "$(grep -vcE '^[a-z_]+=' "$T/gho")" 0
run "garbage"; eq "an unreadable age is left alone" "$rc|$result" "1|refused"

# a workspace that does not exist yet: nothing to unlock in a sweep, an error when asked for explicitly
WS=dev WS_MISSING=1 run "" true
eq "report: a missing workspace is not-locked and exits 0" "$rc|$result" "0|not-locked"
WS=dev WS_MISSING=1 run "" false
eq "manual unlock: a missing workspace is still refused" "$rc|$result" "1|refused"

# the active-job check reads every page (100 per page) and gives up when more than 300 runs are active
: > "$T/ghlog"; GH_LOG="$T/ghlog" STUB_RUNS=5 STUB_JOBS='{"jobs":[]}' run "$(ago 90)" >/dev/null
eq "the runs and jobs listings are paginated with 100 per page" "$(grep -c -- '--paginate' "$T/ghlog")|$(grep -c 'per_page=100' "$T/ghlog")" "4|4"
many=$(seq 1 301 | tr '\n' ' ')
STUB_RUNS="$many" run "$(ago 90)"; eq "more than 300 active runs: refused, the lock is not released" "$rc|$result" "1|refused"

# ---- the probe (state mv -dry-run) and the report-only mode
NO_STATE=1 run ""; eq "no state file at all: not locked" "$rc|$result" "0|not-locked"
PROBE_FAIL=1 run ""; eq "a probe that fails for another reason: refused (cannot tell)" "$rc|$result" "1|refused"
REPORT_ONLY=true run "$(ago 90)"; eq "report only: a lock is shown, not released" "$rc|$result|$(grep -c force-unlock "$T/log")" "0|locked|0"
case "$detail" in *"ci@host"*"OperationTypeApply"*) ok "the report names the holder and the operation";; *) bad "report detail: $detail";; esac
REPORT_ONLY=true run ""; eq "report only: no lock = not-locked" "$rc|$result" "0|not-locked"

done_tests
