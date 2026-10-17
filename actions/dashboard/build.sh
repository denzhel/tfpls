#!/usr/bin/env bash
# build.sh <data-dir> <out-dir>   data-dir = a checkout of the tfpls-data branch (drift/*.json, audit/*.jsonl)
# Writes <out-dir>/index.html (one self-contained page, no external requests) and <out-dir>/summary.md.
# Env: REPO (owner/name, shown in the title), SERVER_URL (default https://github.com), EVENTS (feed length, default 50), TFPLS_NOW.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
data=${1:?usage: build.sh <data-dir> <out-dir>}; out=${2:?usage: build.sh <data-dir> <out-dir>}
mkdir -p "$out"
now="${TFPLS_NOW:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

drift='[]'; audit='[]'
if compgen -G "$data/drift/*.json" >/dev/null; then
  drift=$(for f in "$data"/drift/*.json; do jq -c --arg f "$(basename "$f" .json)" '. + {file:$f}' "$f" 2>/dev/null; done | jq -sc '.')
fi
if compgen -G "$data/audit/*.jsonl" >/dev/null; then
  audit=$(cat "$data"/audit/*.jsonl | jq -Rc 'fromjson? // empty' | jq -sc '.')
fi

# the data goes in through files: a command-line argument over 128 KiB cannot be passed to a process
dj=$(mktemp); aj=$(mktemp); trap 'rm -f "$dj" "$aj"' EXIT
printf '%s' "$drift" > "$dj"; printf '%s' "$audit" > "$aj"
json=$(jq -n --slurpfile dr "$dj" --slurpfile au "$aj" --arg repo "${REPO:-}" --arg now "$now" \
  --arg server "${SERVER_URL:-https://github.com}" --argjson n "${EVENTS:-50}" '
  $dr[0] as $drift | $au[0] as $audit |
  def name_of: .name // (.file | sub("_"; "/"));       # older state files had no name: best guess from the file name
  ($audit | sort_by(.ts) | reverse) as $ev
  | ($ev | map(select(.kind == "apply" and .status == "applied")) | group_by(.workspace) | map({key: .[0].workspace, value: .[0]}) | from_entries) as $applied
  | ( [ $drift[] | name_of as $n | {
          name: $n, stack: ($n | split("/")[0]), state: (.state // "unknown"), since: .since, checked: .checked,
          summary: (.summary // ""), issue: (if .state == "drift" or .state == "error" then .issue else null end),
          issue_url: (if .issue and (.state == "drift" or .state == "error") then "\($server)/\($repo)/issues/\(.issue)" else null end),
          last_apply: ($applied[$n] | if . then {ts, status, actor, run} else null end)
        } ]
      + [ $applied | to_entries[] | select(.key as $k | ($drift | map(name_of) | index($k)) == null)
          | {name: .key, stack: (.key | split("/")[0]), state: "unknown", since: null, checked: null, summary: "", issue: null, issue_url: null,
             last_apply: {ts: .value.ts, status: .value.status, actor: .value.actor, run: .value.run}} ] ) as $ws
  | {generated: $now, repo: $repo, workspaces: $ws, events: $ev[0:$n]}')

# the page: embedded JSON must not be able to close the script element
printf '%s' "$json" | jq -c . | sed 's/</\\u003c/g' > "$out/data.json"   # "<" only occurs inside strings; \u003c is the same text for JSON.parse
jq -rRs --rawfile d "$out/data.json" 'sub("__TFPLS_DATA__"; $d)' "$here/template.html" > "$out/index.html"
rm -f "$out/data.json"

# Markdown for the job summary: same facts, readable without opening the page
jq -r '
  def icon: if . == "in-sync" then "🟢" elif . == "drift" then "🟠" elif . == "error" then "🔴" else "⚪" end;
  "## Infrastructure status — \(.repo)\n\n\(.workspaces | length) workspaces: \(.workspaces | map(select(.state=="in-sync")) | length) in sync, \(.workspaces | map(select(.state=="drift")) | length) drifted, \(.workspaces | map(select(.state=="error")) | length) failing\n\n| Workspace | Status | Pending | Checked |\n|---|---|---|---|\n"
  + ( .workspaces | sort_by(if .state=="error" then 0 elif .state=="drift" then 1 elif .state=="unknown" then 2 else 3 end, .name)
      | map("| `\(.name)` | \(.state | icon) \(.state) | \(if .state == "in-sync" then "" else (.summary | gsub("\\|"; "/")) end) | \(.checked // "–") |") | join("\n") )
  + "\n\n### Recent activity\n\n"
  + ( if (.events | length) == 0 then "_No audit events yet._" else (.events[0:10] | map("- \(.ts) · \(.kind) `\(.workspace)` · \(.status)\(if .actor != "" and .actor != null then " · \(.actor)" else "" end)") | join("\n")) end)
' <<<"$json" > "$out/summary.md"
echo "dashboard built: $(jq '.workspaces | length' <<<"$json") workspaces, $(jq '.events | length' <<<"$json") events"
