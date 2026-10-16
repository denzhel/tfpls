#!/usr/bin/env bash
# The manual `locks` task: look at the state lock of every workspace of ENTRIES (the `entries` output of detect-workspaces, an
# object label -> entry), one unlock.sh process per workspace, one after the other, and say who holds each lock. Nothing is released
# unless a workspace is NAMED in RELEASE (space-separated names or labels): those are released under the rules of /tofu unlock
# (the lock must be at least 5 minutes old and no run of this repository may be active on the workspace). A table goes to the step summary.
# A workspace that cannot be checked is reported and fails the run; the others are still checked.
# Env: ENTRIES, RELEASE, TIMEOUT_MINUTES (per workspace), REPO_ROOT, RUN_URL, GH_TOKEN, GITHUB_OUTPUT, GITHUB_STEP_SUMMARY,
#      AUDIT (true), BRANCH (data branch), REPO, ACTOR, SHA for the audit event of a release.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
limit=()
if command -v timeout >/dev/null 2>&1; then limit=(timeout "${TIMEOUT_MINUTES:-10}m"); fi
RELEASE=${RELEASE:-}
named() { local w; for w in $RELEASE; do [ "$w" = "$1" ] && return 0; [ "$w" = "$2" ] && return 0; done; return 1; }

# one provider download for every workspace of this task, not one per workspace
export TF_PLUGIN_CACHE_DIR="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/tfpls-plugin-cache"; mkdir -p "$TF_PLUGIN_CACHE_DIR"

rows=""; failed=0; locked=0; released=0
while IFS= read -r label; do
  e=$(jq -c --arg l "$label" '.[$l]' <<<"$ENTRIES")
  name=$(jq -r .name <<<"$e")
  f=$(mktemp); mode=report
  if named "$label" "$name"; then mode=release; fi
  echo "::group::$label ($mode)"
  if [ "$mode" = release ]; then flags=(REPORT_ONLY=false); else flags=(REPORT_ONLY=true); fi
  env NAME="$name" LABEL="$label" DIR="$(jq -r .dir <<<"$e")" WS="$(jq -r '.workspace // ""' <<<"$e")" \
      GITHUB_OUTPUT="$f" "${flags[@]}" ${limit[@]+"${limit[@]}"} bash "$here/unlock.sh"
  rc=$?
  echo "::endgroup::"
  result=$(sed -n 's/^result=//p' "$f" | tail -1); detail=$(sed -n 's/^detail=//p' "$f" | tail -1); id=$(sed -n 's/^lock_id=//p' "$f" | tail -1)
  rm -f "$f"
  case "${result:-error}" in
    not-locked) state="free";;
    locked)     state="🔒 locked"; locked=$((locked + 1));;
    unlocked)   state="🔓 released"; released=$((released + 1));;
    kept)       state="kept";;
    *)          state="⚠️ could not check";;
  esac
  [ "$rc" -eq 0 ] || { state="⚠️ could not check or release"; echo "::error::$label: ${detail:-see the log above}"; failed=$((failed + 1)); }
  rows="$rows| \`$label\` | $state | ${detail//|/\\|} |"$'\n'
  if [ "$result" = unlocked ] && [ "${AUDIT:-false}" = true ]; then
    env WORKSPACE="$name" KIND=unlock STATUS=unlocked LOCK_ID="$id" VIA=locks-task SUMMARY="$detail" TOKEN="${GH_TOKEN:-}" TRIGGER="${GITHUB_EVENT_NAME:-}" RUN_URL="${RUN_URL:-}" \
        bash "$here/../record/record.sh" || true
  fi
done < <(jq -r 'keys[]' <<<"$ENTRIES")

{
  echo "### State locks"
  echo
  echo "| Workspace | State | Details |"
  echo "|---|---|---|"
  printf '%s' "$rows"
  echo
  if [ "$locked" -gt 0 ]; then echo "To release a lock that nobody is working on: run the manual task \`locks\` again with \`workspaces\` set to the workspace name (or comment \`/tofu unlock <workspace>\` on a pull request). Nothing is released automatically."; fi
} >> "${GITHUB_STEP_SUMMARY:-/dev/null}"
echo "$locked locked, $released released, $failed failed"
if [ -n "${GITHUB_OUTPUT:-}" ]; then { echo "locked=$locked"; echo "released=$released"; } >> "$GITHUB_OUTPUT"; fi
[ "$failed" -eq 0 ]
