#!/usr/bin/env bash
# Drop the workspaces that forbid a fix (`drift_fix: false`) from the detected entries and say so on the issue.
# Env: ENTRIES (detect output), ISSUE_NUMBER, REPO, GH_TOKEN.  Outputs: matrix, entries, any.
set -uo pipefail
out() { echo "$1=$2" >> "${GITHUB_OUTPUT:-/dev/null}"; }
blocked=$(jq -r '[.[] | select(.drift_fix == "false") | .label] | join(" ")' <<<"$ENTRIES")
kept=$(jq -c 'with_entries(select(.value.drift_fix != "false"))' <<<"$ENTRIES")
if [ -n "$blocked" ]; then
  msg="🚫 \`$blocked\`: fixing a drift on demand is switched off for this workspace (\`drift_fix: false\`); open a pull request instead."
  echo "::warning::$msg"
  [ -n "${ISSUE_NUMBER:-}" ] && gh issue comment "$ISSUE_NUMBER" --repo "$REPO" --body "$msg" >/dev/null 2>&1 || true
fi
out entries "$kept"
out matrix "$(jq -c '{include: [keys[] | {label: .}]}' <<<"$kept")"
out any "$(jq -r 'if length > 0 then "true" else "false" end' <<<"$kept")"
