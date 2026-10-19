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
# actions that still run on Node.js 20 (GitHub forces Node 24 and prints a deprecation warning): none of these pins may come back
node20=(
  actions/checkout@11d5960a326750d5838078e36cf38b85af677262           # v4.4.0
  actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02    # v4.6.2
  actions/upload-pages-artifact@56afc609e74202658d3ffba0e8f6dda462b719fa  # v3.0.1 (uses upload-artifact v4)
  actions/deploy-pages@d6db90164ac5ed86f2b6aed7e0febac5b3c0c03e       # v4.0.5
)
node20_count() { local n=0 p; for p in "${node20[@]}"; do n=$((n + $(grep -rF -- "$p" "$@" 2>/dev/null | wc -l))); done; echo "$n"; }
eq "no Node.js 20 action pin in the workflows, actions and examples" "$(node20_count .github actions examples)" 0
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
printf '      - uses: actions/checkout@11d5960a326750d5838078e36cf38b85af677262 # v4.4.0\n' > "$T/old.yml"
eq "the guard finds a Node.js 20 pin that is put back (fixture)" "$(node20_count "$T")" 1

done_tests
