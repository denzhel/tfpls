#!/usr/bin/env bash
# Remember each workspace's last drift state on the data branch (drift/<name>.json) and act only on changes:
#   in-sync -> drift   => transition "drifted"   (open or refresh the workspace's issue)
#   drift/error -> in-sync => "recovered"        (comment and close the issue)
#   anything -> error  => "failing"              (once, not on every run)
# Best-effort like the audit log: never fails the run. Test overrides: TFPLS_REMOTE_URL, TFPLS_NOW.
set -uo pipefail

outv() { [ -n "${GITHUB_OUTPUT:-}" ] && echo "$1=$2" >> "$GITHUB_OUTPUT"; return 0; }
give_up() { echo "::warning::drift report: $1"; outv transition "${2:-none}"; exit 0; }   # transition "unstored" = the state could not be written, so the workflow alerts on any non-clean result

BRANCH="${BRANCH:-tfpls-data}"
REMOTE="${TFPLS_REMOTE_URL:-https://x-access-token:${TOKEN:-}@github.com/${REPO:-}.git}"
now="${TFPLS_NOW:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
label="${LABEL:-$NAME}"
case "$STATE" in in-sync|drift|error) ;; *) give_up "unknown state \`$STATE\`";; esac
case "$BRANCH" in main|master) give_up "refusing to write to \`$BRANCH\`";; esac
[[ "$BRANCH" =~ ^[A-Za-z0-9._/-]+$ ]] || give_up "invalid branch name"
file="drift/$(printf '%s' "$NAME" | tr '/ ' '__' | tr -cd 'A-Za-z0-9._-').json"

summary=$(printf '%s' "${SUMMARY:-}" | tr '\r\n' '  ' | head -c 300)
export TFPLS_REMOTE_URL="$REMOTE"
# shellcheck source=../record/datastore.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/../record/datastore.sh"

# the writer runs on a fresh clone of the data branch on every attempt (a lost race starts over from the new tip)
prev="in-sync"; since="$now"; issue=""; transition=none
write_state() { # <clone dir>
  local d=$1
  prev="in-sync"; since="$now"; issue=""
  if [ -f "$d/$file" ]; then
    prev=$(jq -r '.state // "in-sync"' "$d/$file" 2>/dev/null || echo in-sync)
    since=$(jq -r '.since // empty' "$d/$file" 2>/dev/null || echo "$now"); [ -n "$since" ] || since="$now"
    issue=$(jq -r '.issue // empty' "$d/$file" 2>/dev/null || true)
  fi
  transition=none
  if [ "$prev" != "$STATE" ]; then
    since="$now"
    case "$STATE" in
      drift) transition=drifted;;
      in-sync) transition=recovered;;
      error) transition=failing;;
    esac
  fi
  mkdir -p "$d/drift"
  jq -n --arg name "$NAME" --arg s "$STATE" --arg since "$since" --arg ts "$now" --arg issue "$issue" --arg sum "$summary" \
    '{name:$name, state:$s, since:$since, checked:$ts, issue:(if $issue == "" then null else ($issue|tonumber) end), summary:$sum}' > "$d/$file"
  [ -f "$d/README.md" ] || printf '# tfpls data\n\nState written by tfpls (`audit/` events, `drift/` last known drift state). Do not edit by hand.\n' > "$d/README.md"
}
tfpls_data_write write_state "drift: $NAME $STATE" || give_up "could not store the drift state after 10 attempts" unstored

outv transition "$transition"

# ---- issue lifecycle (after the state is stored, so a lost race never opens two)
if [ "${ISSUES:-true}" = true ] && { [ "$transition" != none ] || [ "$STATE" = drift ]; }; then
  title="Drift: $label"
  body="**\`$label\`** has drifted from its code.

| | |
|---|---|
| Since | $since |
| Last check | $now |
| Plan | ${summary:-see the run} |

[Latest run]($RUN_URL)

**Fix it without a pull request** (needs write access; the next drift check closes this issue when it is back in sync):
- comment \`/tofu plan\` here to see exactly what would change, then \`/tofu apply\` to apply that plan, or \`/tofu fix\` to plan and apply in one go
- or start the manual run of the workflow with task \`fix\` and workspace \`$label\`

To change the code instead (accept the drift), open a pull request.

This issue is opened when a workspace drifts and closed automatically when it is back in sync."
  open_state=""
  if [ -n "$issue" ]; then open_state=$(gh issue view "$issue" --repo "$REPO" --json state --jq .state 2>/dev/null || true); fi
  case "$STATE" in
    drift)
      if [ "$open_state" = OPEN ]; then
        gh issue edit "$issue" --repo "$REPO" --body "$body" >/dev/null 2>&1 || echo "::warning::drift report: could not refresh issue #$issue"
      else
        gh label create tfpls-drift --repo "$REPO" --color D93F0B --description "Workspace drifted from code" >/dev/null 2>&1 || true
        # an open drift issue of this workspace may exist without being recorded (a lost write): reuse it instead of opening a second one
        existing=$(gh issue list --repo "$REPO" --state open --label tfpls-drift --limit 100 --json number,title 2>/dev/null | jq -r --arg t "$title" '.[] | select(.title == $t) | .number' 2>/dev/null | head -1 || true)
        if [[ "$existing" =~ ^[0-9]+$ ]]; then
          gh issue edit "$existing" --repo "$REPO" --body "$body" >/dev/null 2>&1 || echo "::warning::drift report: could not refresh issue #$existing"
          url="https://github.com/$REPO/issues/$existing"
        else
          url=$(gh issue create --repo "$REPO" --title "$title" --body "$body" --label tfpls-drift 2>/dev/null || true)
        fi
        issue=${url##*/}
        if [[ "$issue" =~ ^[0-9]+$ ]]; then
          # store the issue number (best effort: a second attempt just reuses the open issue by title lookup next time)
          store_issue() { [ -f "$1/$file" ] || return 0; jq --argjson n "$issue" '.issue = $n' "$1/$file" > "$1/$file.new" && mv "$1/$file.new" "$1/$file"; }
          tfpls_data_write store_issue "drift: $NAME issue #$issue" || echo "::warning::drift report: could not store the issue number"
        else echo "::warning::drift report: could not open an issue"; issue=""; fi
      fi;;
    in-sync)
      if [ "$open_state" = OPEN ]; then
        gh issue close "$issue" --repo "$REPO" --comment "Back in sync as of $now ([run]($RUN_URL))." >/dev/null 2>&1 || echo "::warning::drift report: could not close issue #$issue"
      fi;;
    error)
      if [ "$open_state" = OPEN ]; then
        gh issue comment "$issue" --repo "$REPO" --body "The drift check itself failed on $now ([run]($RUN_URL)); the workspace may still be drifted." >/dev/null 2>&1 || true
      fi;;
  esac
fi
outv issue "${issue:-}"
echo "drift: $NAME $prev -> $STATE (transition: $transition)"
