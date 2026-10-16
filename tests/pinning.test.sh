#!/usr/bin/env bash
# Every third-party action must be pinned to a full commit SHA (supply-chain safety). Our own repo's
# references (denzhel/tfpls/...@main on main, @<version> in releases) and local ./ paths are allowed.
# Run: bash tests/pinning.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
cd "$here" || exit 1
files=$(ls .github/workflows/*.yml actions/*/action.yml examples/.tfpls.yml 2>/dev/null)
n=0
while IFS= read -r line; do
  f=${line%%:*}; ref=$(sed -E 's/^[^:]*:[0-9]+:[[:space:]]*(-[[:space:]]*)?uses:[[:space:]]*([^[:space:]]+).*$/\2/' <<<"$line")
  n=$((n+1))
  case "$ref" in
    ./*|denzhel/tfpls/*) ok "internal/local ref ok: $ref ($f)";;
    *@????????????????????????????????????????) [[ "${ref##*@}" =~ ^[0-9a-f]{40}$ ]] && ok "pinned: $ref" || bad "not a hex SHA: $ref ($f)";;
    *) bad "third-party action not pinned to a SHA: $ref ($f)";;
  esac
done < <(grep -nE '^[[:space:]]*(-[[:space:]]*)?uses:' $files | sed -E 's/^([^:]+):([0-9]+):/\1:\2:/')
[ "$n" -gt 0 ] && ok "scanned $n uses: lines" || bad "found no uses: lines"
done_tests
