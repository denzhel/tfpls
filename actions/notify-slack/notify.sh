#!/usr/bin/env bash
# Post a status message to an incoming webhook: Slack (default), Microsoft Teams, or generic JSON.
# Env: HOOK, FORMAT (slack|teams|generic), MENTION (text prepended on failures, e.g. "<!subteam^S123>" for Slack),
#      STATUS, NAME, KIND, SUMMARY, URL, REPO, PR, CHANGES (JSON [{action,address}]), CHANGES_MORE,
#      AUTHOR, COMMIT_MESSAGE, COMMIT_URL, SHA, TEMPLATE (path of a jq file), TEMPLATE_REF (read it from this ref of REPO
#      instead of the checkout), WORKSPACE_DIR, GH_TOKEN.  Never fails the job.
# The webhook body is the output of default.jq, or of the repository's own template; the template reads the run JSON
# (docs/notifications.md) and runs without any environment.
set -uo pipefail
[ -n "${HOOK:-}" ] || exit 0
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
FORMAT="${FORMAT:-slack}"
case "$STATUS" in
  applied|no-changes|unlocked|in-sync) good=1; emoji="✅";;
  planned|skipped) good=1; emoji="📝";;
  drift|drifted) good=0; emoji="⚠️";;
  *) good=0; emoji="❌";;
esac
# only call people when something is wrong
ping=""; [ "$good" -eq 0 ] && [ -n "${MENTION:-}" ] && ping="$MENTION "
summary=$(printf '%s' "${SUMMARY:-}" | tr '\r\n' '  ' | head -c 500)
PR="${PR:-}"
kind_text="$KIND"; [ -n "$PR" ] && kind_text="$KIND (PR #$PR)"
case "$KIND" in apply*) kind_id=apply;; drift*) kind_id=drift;; *lock*) kind_id=unlock;; *) kind_id="$KIND";; esac
msg_line=$(printf '%s' "${COMMIT_MESSAGE:-}" | head -n1 | head -c 200)

changes=$(jq -c 'if type == "array" then [.[] | select(type == "object") | {action: (.action | tostring), address: (.address | tostring)}] else [] end' <<<"${CHANGES:-[]}" 2>/dev/null) || changes="[]"
more=$(jq -nr --argjson c "$changes" --arg m "${CHANGES_MORE:-0}" '[(($m | tonumber? // 0)), ($c | length - 50), 0] | max')
changes=$(jq -c '.[0:50]' <<<"$changes")

run_json=$(jq -n -c --arg repo "$REPO" --arg workspace "$NAME" --arg kind "$kind_id" --arg status "$STATUS" --arg summary "$summary" \
  --argjson ok "$([ "$good" -eq 1 ] && echo true || echo false)" --argjson changes "$changes" --argjson more "$more" \
  --arg author "${AUTHOR:-}" --arg sha "${SHA:-}" --arg message "$msg_line" --arg curl "${COMMIT_URL:-}" \
  --arg pr "$PR" --arg run_url "$URL" --arg mention "$ping" --arg counts "${COUNTS:-}" \
  '{repo: $repo, workspace: $workspace, kind: $kind, status: $status, ok: $ok, summary: $summary,
    counts: ($counts | try fromjson catch null), changes: $changes, changes_more: $more, author: $author,
    commit: {sha: $sha, message: $message, url: $curl}, pr: (if $pr == "" then null else ($pr | tonumber? // $pr) end),
    run_url: $run_url, mention: $mention}')
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
printf '%s\n' "$run_json" > "$T/run.json"
jqbin=$(command -v jq); timeoutbin=$(command -v timeout || command -v gtimeout || true)

# run a jq program over the run JSON with an empty environment; prints its output lines, returns jq's status
render() {
  if [ -n "$timeoutbin" ]; then env -i "$timeoutbin" 10 "$jqbin" -c -f "$1" "$T/run.json"
  else env -i "$jqbin" -c -f "$1" "$T/run.json"; fi
}
# a usable body is exactly one JSON object
one_object() { [ "$(grep -c . <<<"$1")" -eq 1 ] && jq -e 'type == "object"' <<<"$1" >/dev/null 2>&1; }

tfile=""
if [ -n "${TEMPLATE:-}" ]; then
  case "$TEMPLATE" in /*|*..*) echo "::warning::notify_template \`$TEMPLATE\` is not a relative path inside the repository; using the default message";;
  *)
    if [ -n "${TEMPLATE_REF:-}" ]; then
      # read it from the default branch: a pull request must not change the message about its own apply
      if gh api -H 'Accept: application/vnd.github.raw' "repos/$REPO/contents/$TEMPLATE?ref=$TEMPLATE_REF" > "$T/template.jq" 2>/dev/null && [ -s "$T/template.jq" ]; then tfile="$T/template.jq"
      else echo "::warning::could not read notify_template \`$TEMPLATE\` from \`$TEMPLATE_REF\`; using the default message"; fi
    elif [ -f "${WORKSPACE_DIR:-.}/$TEMPLATE" ]; then tfile="${WORKSPACE_DIR:-.}/$TEMPLATE"
    else echo "::warning::notify_template \`$TEMPLATE\` not found; using the default message"; fi;;
  esac
fi

body=""; have_body=false
if [ -n "$tfile" ]; then
  if out=$(render "$tfile" 2>"$T/err"); then
    if [ -z "$(tr -d '[:space:]' <<<"$out")" ] || [ "$out" = null ]; then echo "notify_template produced no message for $NAME ($STATUS); nothing sent"; exit 0; fi
    if one_object "$out"; then body=$out; have_body=true
    else echo "::warning::notify_template must output exactly one JSON object; using the default message"; fi
  else echo "::warning::notify_template failed ($(head -c 200 "$T/err" | tr '\n' ' ')); using the default message"; fi
fi

if [ "$have_body" = false ]; then
  case "$FORMAT" in
    slack)
      if ! out=$(render "$here/default.jq" 2>/dev/null); then echo "::warning::default message could not be built"; exit 0; fi
      if [ -z "$(tr -d '[:space:]' <<<"$out")" ]; then echo "no message for $NAME ($STATUS); nothing sent"; exit 0; fi
      body=$out;;
    teams)   body=$(jq -n --arg t "$ping$emoji **$REPO** / \`$NAME\` — $kind_text: **$STATUS**  $summary  [logs]($URL)" --arg s "$REPO $NAME $kind_text $STATUS" \
               '{"@type":"MessageCard","@context":"http://schema.org/extensions",summary:$s,themeColor:"0076D7",text:$t}');;
    generic) body=$(jq -c --arg summary "$summary" '{repo, workspace, kind, status, ok, summary: $summary, url: .run_url, author, commit, changes, changes_more}' "$T/run.json");;
    *) echo "::warning::unknown notification format \`$FORMAT\` (slack, teams or generic)"; exit 0;;
  esac
fi
# the message goes to the log too (addresses and names only, no plan values), so a run can be checked without Slack
echo "::group::message for $NAME ($STATUS)"; printf '%s\n' "$body" | head -c 3000; echo; echo "::endgroup::"
if printf '%s' "$body" | curl -fsS -X POST -H 'Content-type: application/json' --data @- "$HOOK" >/dev/null; then
  echo "$FORMAT notification sent for $NAME ($STATUS)"      # the webhook accepted it (HTTP 2xx)
else
  echo "::warning::$FORMAT notification failed"
fi
exit 0
