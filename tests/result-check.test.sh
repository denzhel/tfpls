#!/usr/bin/env bash
# Tests for the optional aggregated result check (actions/result): runs the real script. Run: bash tests/result-check.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
script="$here/../actions/result/result.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
check() { # name expected-rc needs-json
  NEEDS="$3" bash "$script" >/dev/null 2>&1; rc=$?
  if { [ "$2" = 0 ] && [ $rc -eq 0 ]; } || { [ "$2" != 0 ] && [ $rc -ne 0 ]; }; then ok "$1"; else bad "$1 (rc=$rc)"; fi
}
check "all plans succeeded => green"          0 '{"detect":{"result":"success"},"plan":{"result":"success"}}'
check "nothing selected (plan skipped) => green" 0 '{"detect":{"result":"success"},"plan":{"result":"skipped"}}'
check "one workspace plan failed => red"      1 '{"detect":{"result":"success"},"plan":{"result":"failure"}}'
check "plan cancelled => red"                 1 '{"detect":{"result":"success"},"plan":{"result":"cancelled"}}'
check "detect failed => red"                  1 '{"detect":{"result":"failure"},"plan":{"result":"skipped"}}'
[ "$(yq -r '.jobs.result // "none"' "$here/../.github/workflows/plan.yml")" = none ] && ok "plan.yml adds no result row by default (opt-in via actions/result)" || bad "plan.yml still has a result job"
[ "$(yq -r '.runs.steps[0].run' "$here/../actions/result/action.yml")" != "" ] && ok "action runs the script" || bad "action"
done_tests
