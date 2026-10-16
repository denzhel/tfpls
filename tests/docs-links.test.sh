#!/usr/bin/env bash
# Every relative Markdown link in README, CONTRIBUTING, SECURITY and docs/ must point at a file that exists.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$here" || exit 1
pass=0; failn=0
for f in README.md CONTRIBUTING.md SECURITY.md CHANGELOG.md docs/*.md; do
  d=$(dirname "$f")
  while IFS= read -r link; do
    t=${link%%#*}; [ -n "$t" ] || continue
    case "$t" in http://*|https://*|mailto:*) continue;; esac
    if [ -e "$d/$t" ]; then pass=$((pass+1)); else failn=$((failn+1)); echo "FAIL $f links to missing $link"; fi
  done < <(grep -oE '\]\([^)]+\)' "$f" | sed -E 's/^\]\(//; s/\)$//' | grep -vE '^`')
done
echo "ok   $pass relative links resolve"
echo; echo "$pass passed, $failn failed"; [ "$failn" -eq 0 ]
