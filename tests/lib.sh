#!/usr/bin/env bash
# Shared by the test files: counters, ok/bad/eq, a polling wait and the final line.
#   . "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# It sets no traps and no shell options: every test file keeps its own (`set -uo pipefail`, its own cleanup trap).
pass=0; failn=0
ok()  { pass=$((pass+1)); echo "ok   $1"; }
bad() { failn=$((failn+1)); echo "FAIL $1"; }
eq()  { [ "$2" = "$3" ] && ok "$1" || bad "$1 (got '$2', want '$3')"; }
# wait_for <file> <pattern> [seconds]: poll (every 0.2 s) until the file contains the pattern; returns 1 on timeout
wait_for() {
  local f=$1 pat=$2 max=${3:-15} i=0
  while [ "$i" -lt $((max * 5)) ]; do
    grep -q -- "$pat" "$f" 2>/dev/null && return 0
    sleep 0.2; i=$((i + 1))
  done
  return 1
}
# build_bin <tfpls|release|toolversions> <dest> [go build flags]: put the binary of ./cmd/<name> at <dest>.
# tests/run-all.sh builds each once and exports TFPLS_TEST_BIN / TFPLS_TEST_RELEASE_BIN / TFPLS_TEST_TOOLVERSIONS_BIN;
# a file run by hand (or one that passes its own flags, e.g. -ldflags) builds it itself.
build_bin() {
  local name=$1 dest=$2 pre="" root; shift 2
  root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
  case "$name" in
    tfpls) pre=${TFPLS_TEST_BIN:-} ;;
    release) pre=${TFPLS_TEST_RELEASE_BIN:-} ;;
    toolversions) pre=${TFPLS_TEST_TOOLVERSIONS_BIN:-} ;;
  esac
  mkdir -p "$(dirname "$dest")"
  if [ "$#" -eq 0 ] && [ -n "$pre" ] && [ -x "$pre" ]; then cp "$pre" "$dest"; return; fi
  (cd "$root" && go build -o "$dest" "$@" "./cmd/$name")
}
done_tests() { echo; echo "$pass passed, $failn failed"; [ "$failn" -eq 0 ]; }
