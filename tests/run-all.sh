#!/usr/bin/env bash
# Run every tests/*.test.sh in parallel (TFPLS_TEST_JOBS, default 4; 1 = one after the other); fail if any fails.
# Output is printed per file, in order. Run: bash tests/run-all.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
out=$(mktemp -d); trap 'rm -rf "$out"' EXIT
export out
run_one() { t=$1; n=$(basename "$t"); bash "$t" > "$out/$n.log" 2>&1; echo $? > "$out/$n.rc"; }
export -f run_one
find "$here" -maxdepth 1 -name '*.test.sh' | sort | xargs -P "${TFPLS_TEST_JOBS:-4}" -I{} bash -c 'run_one {}'
failed=0
for t in "$here"/*.test.sh; do
  n=$(basename "$t")
  echo "=== $n"; cat "$out/$n.log"
  [ "$(cat "$out/$n.rc" 2>/dev/null || echo 1)" = 0 ] || { echo "!!! $n FAILED"; failed=$((failed+1)); }
done
echo; if [ "$failed" -eq 0 ]; then echo "all test files passed"; else echo "$failed test file(s) failed"; fi
[ "$failed" -eq 0 ]
