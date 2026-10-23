#!/usr/bin/env bash
# Tests for the optional aggregated result check (actions/result): runs the real script. Run: bash tests/result-check.test.sh
set -uo pipefail
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build_bin tfpls "$T/tfpls" || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
check() { # name expected-rc needs-json
  NEEDS="$3" "$T/tfpls" result >/dev/null 2>&1; rc=$?
  if { [ "$2" = 0 ] && [ $rc -eq 0 ]; } || { [ "$2" != 0 ] && [ $rc -ne 0 ]; }; then ok "$1"; else bad "$1 (rc=$rc)"; fi
}
check "all plans succeeded => green"          0 '{"detect":{"result":"success"},"plan":{"result":"success"}}'
check "nothing selected (plan skipped) => green" 0 '{"detect":{"result":"success"},"plan":{"result":"skipped"}}'
check "one workspace plan failed => red"      1 '{"detect":{"result":"success"},"plan":{"result":"failure"}}'
check "plan cancelled => red"                 1 '{"detect":{"result":"success"},"plan":{"result":"cancelled"}}'
check "detect failed => red"                  1 '{"detect":{"result":"failure"},"plan":{"result":"skipped"}}'
done_tests
