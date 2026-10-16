#!/usr/bin/env bash
# rollback.sh <apply.txt> [commit-sha]  -> Markdown hints after a failed apply: what was already changed and how to get back.
set -uo pipefail
f=${1:?usage: rollback.sh <apply.txt> [sha]}; sha=${2:-}
done_=$(grep -E ': (Creation|Modifications|Destruction) complete' "$f" | sed -E 's/\x1b\[[0-9;]*m//g; s/: (Creation|Modifications|Destruction) complete.*/ (\1)/; s/Modifications/Modification/; s/^ *//' | sort -u)
errs=$(sed -E 's/\x1b\[[0-9;]*m//g' "$f" | grep -E '^(│ )?Error: ' | sed -E 's/^│ //' | sort -u | head -5)
n=$(printf '%s' "$done_" | grep -c . || true)
printf '**The apply failed part-way.** Terraform state already contains what was finished; nothing is rolled back automatically.\n\n'
if [ "$n" -gt 0 ]; then
  printf 'Already changed before the failure (%s):\n' "$n"
  printf '%s\n' "$done_" | head -20 | sed 's/^/- `/; s/ (/` (/'
  [ "$n" -gt 20 ] && printf -- '- … %s more\n' "$((n - 20))"
  printf '\n'
else
  printf 'No resource was changed before the failure.\n\n'
fi
if [ -n "$errs" ]; then printf 'Errors:\n'; printf '%s\n' "$errs" | sed 's/^/- /'; printf '\n'; fi
printf 'What you can do:\n'
printf -- '1. **Fix forward**: correct the cause (permissions, quota, a conflicting resource) and run `/tofu plan` again; the next plan shows only what is left.\n'
if [ -n "$sha" ]; then printf -- '2. **Go back**: revert the change (`git revert %s`) in a new pull request; its plan shows exactly what the revert would undo, and applying it restores the previous configuration.\n' "${sha:0:12}"
else printf -- '2. **Go back**: revert the change in a new pull request; its plan shows exactly what the revert would undo.\n'; fi
printf -- '3. Do not `force-unlock` or delete state by hand unless `/tofu unlock` reports a stale lock.\n'
