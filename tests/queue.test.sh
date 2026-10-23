#!/usr/bin/env bash
# Tests for `tfpls queue` (actions/queue): strict FIFO per workspace, crashed holders, no cancellation.
# A local bare repository stands in for GitHub; a gh stub reports which runs are still active.
set -uo pipefail
export TFPLS_RETRY_SLEEP=0   # retries pause randomly in the scripts under test; the tests do not wait
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'pkill -P $$ 2>/dev/null; rm -rf "$T"' EXIT
(cd "$here" && go build -o "$T/tfpls" ./cmd/tfpls) || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
Q="$T/tfpls"
git init -q --bare "$T/remote.git"; mkdir -p "$T/bin" "$T/active"
# gh stub: runs/<id> is "completed" unless $T/active/<id> exists; comments are logged
cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
case "$*" in
  *"actions/runs/"*) id=$(sed -n 's|.*actions/runs/\([0-9]*\).*|\1|p' <<<"$*"); if [ -f "$STUB_DIR/active/$id" ]; then echo in_progress; else echo completed; fi;;
  *"issues/"*"/comments"*) echo "$*" >> "$STUB_DIR/comments"; echo 777;;
esac
exit 0
STUB
chmod +x "$T/bin/gh"
export TFPLS_REMOTE_URL="$T/remote.git" TFPLS_POLL=1 STUB_DIR="$T" PATH="$T/bin:$PATH" REPO=o/r RUN_URL=http://run BRANCH=tfpls-data TOKEN=x GH_TOKEN=x RUNNER_TEMP="$T"
q() { # mode run name [extra env...] ; runs in the foreground
  local mode=$1 run=$2 name=$3; shift 3
  env MODE="$mode" RUN_ID="$run" NAME="$name" LABEL="$name" "$@" "$Q" queue
}
tickets() { git --git-dir="$T/remote.git" ls-tree --name-only "tfpls-data:queue/$1/" 2>/dev/null | wc -l | tr -d ' '; }

touch "$T/active/101"
q enter 101 ws >"$T/o1" 2>&1; eq "the first run gets its turn immediately" "$?|$(grep -c 'our turn' "$T/o1")" "0|1"
eq "and holds one ticket" "$(tickets ws)" 1

touch "$T/active/102" "$T/active/103"
q enter 102 ws TIMEOUT=1 >"$T/o2" 2>&1 & p2=$!
wait_for "$T/o2" "position 2" 20
q enter 103 ws TIMEOUT=1 >"$T/o3" 2>&1 & p3=$!
wait_for "$T/o3" "position 3" 20
eq "a second run waits at position 2" "$(grep -q "position 2" "$T/o2" && echo yes)" yes
eq "a third run waits at position 3 (not cancelled)" "$(grep -q "position 3" "$T/o3" && echo yes)" yes
eq "three tickets exist" "$(tickets ws)" 3
eq "nobody got a turn yet" "$(grep -c 'our turn' "$T/o2" "$T/o3" | tr '\n' ',')" "$T/o2:0,$T/o3:0,"

q leave 101 ws >/dev/null 2>&1; rm -f "$T/active/101"
wait_for "$T/o2" "our turn" 20
eq "when the first leaves, the second goes next" "$(grep -c 'our turn' "$T/o2")|$(grep -c 'our turn' "$T/o3")" "1|0"
q leave 102 ws >/dev/null 2>&1; rm -f "$T/active/102"
wait_for "$T/o3" "our turn" 20
eq "then the third, in order" "$(grep -c 'our turn' "$T/o3")" 1
wait "$p2" "$p3"
q leave 103 ws >/dev/null 2>&1
eq "all tickets are removed at the end" "$(tickets ws)" 0

# a holder that crashed (run completed, ticket left behind) does not block the queue
touch "$T/active/201"; q enter 201 crash >/dev/null 2>&1; rm -f "$T/active/201"       # run 201 finished without leaving
touch "$T/active/202"
q enter 202 crash TIMEOUT=1 >"$T/o4" 2>&1; eq "a ticket of a finished run is ignored" "$?|$(grep -c 'our turn' "$T/o4")" "0|1"

# queues are per workspace
touch "$T/active/301" "$T/active/302"
q enter 301 a >/dev/null 2>&1; q enter 302 b TIMEOUT=1 >"$T/o5" 2>&1
eq "another workspace has its own queue" "$?|$(grep -c 'our turn' "$T/o5")" "0|1"

# PR comment with the position, removed when the turn comes
rm -f "$T/comments"; touch "$T/active/401" "$T/active/402"
q enter 401 pr >/dev/null 2>&1
( sleep 4; q leave 401 pr >/dev/null 2>&1; rm -f "$T/active/401" ) &
q enter 402 pr PR=12 TIMEOUT=1 >"$T/o6" 2>&1; wait
eq "waiting on a PR leaves a position comment" "$(grep -c 'issues/12/comments' "$T/comments")" 1
eq "and the turn arrives" "$(grep -c 'our turn' "$T/o6")" 1

# gives up after the timeout, never runs out of turn
touch "$T/active/501" "$T/active/502"
q enter 501 slow >/dev/null 2>&1
q enter 502 slow TIMEOUT=0 >"$T/o7" 2>&1; eq "a timeout fails the job with a message" "$?|$(grep -c 'gave up waiting' "$T/o7")" "1|1"

# never blocks on infrastructure problems
TFPLS_REMOTE_URL="$T/missing.git" q enter 601 x >"$T/o8" 2>&1; eq "an unreachable data branch does not block the run" "$?" 0
BRANCH=main q enter 602 x >"$T/o9" 2>&1; eq "refuses to use main" "$?|$(grep -c refusing "$T/o9")" "0|1"
q leave 999 never >/dev/null 2>&1; eq "leave without a ticket is a no-op" "$?" 0
# the wait budget formula is unit-tested in internal/queue (TestBudget)
eq "action.yml installs the binary, then runs tfpls queue" "$(yq -r '.runs.steps[0].uses' "$here/actions/queue/action.yml")|$(yq -r '.runs.steps[1].run' "$here/actions/queue/action.yml")" "denzhel/tfpls/actions/bin@main|tfpls queue"

done_tests
