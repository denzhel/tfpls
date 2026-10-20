#!/usr/bin/env bash
# Append one JSON line to audit/YYYY-MM.jsonl on the data branch (an orphan branch, created on first use).
# Best-effort by design: a failure to write the log must never fail a deploy, so this always exits 0.
# KEPT ONLY for finish/store.sh (sourced) until finish is ported; the action itself runs `tfpls record` (internal/record). Also sourced by finish/store.sh (functions record_prepare and record_write). Test overrides: TFPLS_REMOTE_URL (instead of github.com), TFPLS_NOW (ISO timestamp).
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=datastore.sh
. "$here/datastore.sh"

# validates the environment and builds the event; prints a warning and returns 1 when nothing should be written
record_prepare() {
  BRANCH="${BRANCH:-tfpls-data}"
  now="${TFPLS_NOW:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
  case "$BRANCH" in main|master) echo "::warning::audit log: refusing to write to \`$BRANCH\`"; return 1;; esac
  [[ "$BRANCH" =~ ^[A-Za-z0-9._/-]+$ ]] || { echo "::warning::audit log: invalid branch name"; return 1; }
  [[ "${WORKSPACE:-}" != "" && "${KIND:-}" != "" && "${STATUS:-}" != "" ]] || { echo "::warning::audit log: workspace, kind and status are required"; return 1; }
  counts="${COUNTS:-}"
  jq -e 'type == "object"' <<<"${counts:-null}" >/dev/null 2>&1 || counts='null'

  # keep the one-line summary short and free of line breaks
  summary=$(printf '%s' "${SUMMARY:-}" | tr '\r\n' '  ' | head -c 300)

  event=$(jq -cn \
    --arg ts "$now" --arg repo "${REPO:-}" --arg ws "$WORKSPACE" --arg kind "$KIND" --arg status "$STATUS" \
    --arg actor "${ACTOR:-}" --arg trigger "${TRIGGER:-}" --arg pr "${PR:-}" --arg sha "${SHA:-}" \
    --arg run "${RUN_URL:-}" --arg lock "${LOCK_ID:-}" --arg via "${VIA:-}" --arg dur "${DURATION:-}" --arg reviewed "${REVIEWED:-}" --arg summary "$summary" --argjson counts "$counts" \
    '{ts:$ts, repo:$repo, workspace:$ws, kind:$kind, status:$status, actor:$actor, trigger:$trigger,
      pr:(if $pr == "" then null else ($pr | tonumber? // $pr) end), sha:$sha, run:$run,
      reviewed:(if $reviewed == "" then null else $reviewed == "true" end), counts:$counts, summary:$summary}
      + (if $lock == "" then {} else {lock_id: $lock} end)
      + (if $via == "" then {} else {via: $via} end)
      + (if $dur == "" then {} else {duration_s: ($dur | tonumber? // null)} end)') \
    || { echo "::warning::audit log: could not build the event"; return 1; }

  file="audit/${now:0:7}.jsonl"
}
record_write() {
  mkdir -p "$1/audit"
  printf '%s\n' "$event" >> "$1/$file"
  [ -f "$1/README.md" ] || printf '# tfpls data\n\nAudit log (`audit/YYYY-MM.jsonl`, one JSON event per line) written by tfpls. Do not edit by hand.\n' > "$1/README.md"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  set -uo pipefail
  record_prepare || exit 0
  if tfpls_data_write record_write "audit: $KIND $WORKSPACE $STATUS"; then echo "audit event recorded in $BRANCH:$file"
  else echo "::warning::audit log: could not push to $BRANCH"; fi
  exit 0
fi
