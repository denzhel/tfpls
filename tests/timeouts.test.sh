#!/usr/bin/env bash
# Every job in every workflow must have a timeout (a hung runner or tofu must not run for hours).
# Run: bash tests/timeouts.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
for f in "$here"/.github/workflows/*.yml; do
  for j in $(yq -r '.jobs | keys | .[]' "$f"); do
    # jobs that only call a reusable workflow (uses:) cannot have a timeout themselves
    if [ "$(yq -r ".jobs.\"$j\".uses // \"\"" "$f")" != "" ]; then continue; fi
    t=$(yq -r ".jobs.\"$j\".\"timeout-minutes\" // \"\"" "$f")
    if [ -n "$t" ]; then ok "$(basename "$f") job $j has timeout-minutes ($t)"; else bad "$(basename "$f") job $j has no timeout-minutes"; fi
  done
done
for f in plan apply drift comment-ops; do
  d=$(yq -r '.on.workflow_call.inputs.timeout_minutes.default // ""' "$here/.github/workflows/$f.yml")
  [ "$d" = 60 ] && ok "$f.yml: timeout_minutes input defaults to 60" || bad "$f.yml: timeout_minutes default ($d)"
done
done_tests
