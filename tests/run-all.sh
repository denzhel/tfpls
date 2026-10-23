#!/usr/bin/env bash
# Run every tests/*.test.sh in parallel (TFPLS_TEST_JOBS, default 4; 1 = one after the other); fail if any fails.
# Output is printed per file, in order. Run: bash tests/run-all.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
out=$(mktemp -d); bins=$(mktemp -d); trap 'rm -rf "$out" "$bins"' EXIT
export out
# build each binary once; tests/lib.sh (build_bin) copies these instead of running `go build` per file
(cd "$here/.." && go build -o "$bins/tfpls" ./cmd/tfpls && go build -o "$bins/release" ./cmd/release && go build -o "$bins/toolversions" ./cmd/toolversions) \
  || { echo "go build failed"; exit 1; }
export TFPLS_TEST_BIN="$bins/tfpls" TFPLS_TEST_RELEASE_BIN="$bins/release" TFPLS_TEST_TOOLVERSIONS_BIN="$bins/toolversions"
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
