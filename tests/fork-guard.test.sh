#!/usr/bin/env bash
# The fork-PR guard of plan.yml: runs the real `tfpls glue fork-check`. Run: bash tests/fork-guard.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
build_bin tfpls "$T/tfpls" || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
[ "$(yq -r '.jobs.detect.steps[] | select(.id == "fork") | .run' "$here/.github/workflows/plan.yml")" = "tfpls glue fork-check" ] && ok "plan.yml runs the fork guard command" || { bad "no fork step"; exit 1; }
run() { : > "$T/out"; : > "$T/sum"; OUT=$(env -i PATH="$PATH" GITHUB_OUTPUT="$T/out" GITHUB_STEP_SUMMARY="$T/sum" GITHUB_REPOSITORY=o/r HEAD_REPO="$1" "$T/tfpls" glue fork-check 2>&1); rc=$?; }
run "o/r";        grep -q "is_fork=false" "$T/out" && [ $rc -eq 0 ] && ok "same-repo pull request is planned" || bad "same repo: $OUT"
run "someone/r";  grep -q "is_fork=true" "$T/out"  && [ $rc -eq 0 ] && ok "fork pull request is detected" || bad "fork: $OUT"
case "$OUT" in *"::notice title=Fork pull request not planned::"*"someone/r"*"receive no credentials"*) ok "a notice explains why and names the fork";; *) bad "notice: $OUT";; esac
grep -q "Fork pull request not planned" "$T/sum" && ok "the job summary says so too" || bad "summary"
run "";           grep -q "is_fork=false" "$T/out" && ok "no head repo information (not a pull request event) is not treated as a fork" || bad "empty head repo"
# the steps that need credentials or comments are skipped for forks
[ "$(yq -r '.jobs.detect.steps[] | select(.id == "d") | .if' "$here/.github/workflows/plan.yml")" = "steps.fork.outputs.is_fork != 'true'" ] && ok "detection is skipped for forks, so the plan jobs are skipped" || bad "detect not guarded"
done_tests
