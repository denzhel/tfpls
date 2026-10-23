#!/usr/bin/env bash
# Tests for `tfpls wait-for` (actions/wait-for) with a gh stub that serves a scripted sequence of job lists.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT; mkdir -p "$T/bin"
build_bin tfpls "$T/tfpls" || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
# the stub returns the Nth file of $T/seq (jobs lines) on the Nth call, then keeps returning the last one
cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
n=$(cat "$STUB_DIR/n" 2>/dev/null || echo 0); n=$((n+1)); echo "$n" > "$STUB_DIR/n"
f="$STUB_DIR/seq/$n"; [ -f "$f" ] || f=$(ls "$STUB_DIR"/seq/* | sort -V | tail -1)
cat "$f"
STUB
chmod +x "$T/bin/gh"
seq_() { rm -rf "$T/seq" "$T/n"; mkdir -p "$T/seq"; i=0; for l in "$@"; do i=$((i+1)); printf '%b\n' "$l" > "$T/seq/$i"; done; }
run() { WAIT_FOR="$1" TIMEOUT="${2:-1}" REPO=o/r RUN_ID=1 TFPLS_POLL=0 STUB_DIR="$T" PATH="$T/bin:$PATH" "$T/tfpls" wait-for >"$T/out" 2>&1; rc=$?; }
j() { printf '{"name":"apply / %s","status":"%s","conclusion":%s}' "$1" "$2" "${3:-null}"; }

seq_ "$(j prod completed '"success"')"
run prod; eq "a succeeded predecessor lets the job continue" "$rc" 0
seq_ "$(j prod in_progress)" "$(j prod in_progress)" "$(j prod completed '"success"')"
run prod; eq "waits while the predecessor runs, then continues" "$rc|$(grep -c 'still waiting' "$T/out")" "0|2"
seq_ "$(j prod completed '"failure"')"
run prod; eq "a failed predecessor stops the job" "$rc|$(grep -c 'has to succeed first and finished as failure' "$T/out")" "1|1"
seq_ "$(j prod completed '"cancelled"')"
run prod; eq "a cancelled predecessor stops the job" "$rc" 1
seq_ "$(j prod completed '"skipped"')"
run prod; eq "a skipped predecessor stops the job" "$rc" 1
seq_ "$(j other completed '"success"')" "$(j other completed '"success"')\n$(j prod completed '"success"')"
run prod; eq "a predecessor that has not started yet is waited for" "$rc" 0
seq_ "$(j a completed '"success"')\n$(j b in_progress)" "$(j a completed '"success"')\n$(j b completed '"success"')"
run a,b; eq "several predecessors must all succeed" "$rc" 0
seq_ "$(j a completed '"failure"')\n$(j b in_progress)"
run a,b; eq "one failed predecessor is enough to stop, without waiting for the rest" "$rc" 1
seq_ "$(j prod in_progress)"
TFPLS_POLL=1 run prod 0; eq "gives up after the timeout" "$rc|$(grep -c 'gave up' "$T/out")" "1|1"
seq_ ""
run prod 0; eq "an unreadable job list is retried, then times out" "$rc" 1
# job names: the exact name or the tail of "caller / label"
printf '%s\n' '{"name":"prod","status":"completed","conclusion":"success"}' > "$T/seq/1"
run prod; eq "a plain job name matches too" "$rc" 0
printf '%s\n' '{"name":"apply / preprod","status":"completed","conclusion":"success"}' > "$T/seq/1"
run prod 0; eq "a name that merely ends with the label does not match" "$rc" 1

printf '%s\n' '{"name":"apply (prod)","status":"completed","conclusion":"success"}' > "$T/seq/1"
run prod; eq "the default matrix job name 'apply (prod)' matches" "$rc" 0
printf '%s\n' '{"name":"apply (preprod)","status":"completed","conclusion":"success"}' > "$T/seq/1"
run prod 0; eq "'apply (preprod)' does not match prod" "$rc" 1

eq "action.yml installs the binary, then runs tfpls wait-for" "$(yq -r '.runs.steps[0].uses' "$here/actions/wait-for/action.yml")|$(yq -r '.runs.steps[1].run' "$here/actions/wait-for/action.yml")" "denzhel/tfpls/actions/bin@main|tfpls wait-for"

done_tests
