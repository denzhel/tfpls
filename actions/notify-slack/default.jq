# Default Slack message. Input: the run JSON (see docs/notifications.md); output: the webhook body, or nothing.
def bt: gsub("`"; "'");
def cut($n): if length > $n then .[0:$n] + "…" else . end;
def by:
  if (.commit.message // "") != "" then "By @\(.author), <\(.commit.url)|Commit Message>: `\(.commit.message | bt)`"
  else "By @\(.author)" end;
def logs: "<\(.run_url)|Review Logs>";
def block:
  if (.changes | length) > 0 then
    "\n```\n" + ([.changes[] | "\(.action): \(.address | bt)"] | join("\n"))
    + (if (.changes_more // 0) > 0 then "\n… \(.changes_more) more, see Review Logs" else "" end) + "\n```"
  else "" end;
def head:
  if .kind == "unlock" then "Released a stale lock on `\(.workspace)`"
  elif .kind == "drift" then
    (if .status == "in-sync" then "Drift on `\(.workspace)` is resolved"
     elif (.status | IN("drift", "drifted")) then "\(.mention // "")Drift detected on `\(.workspace)`"
     else "\(.mention // "")A drift run on `\(.workspace)` has failed" end)
  elif .status == "applied" then "Successful deployment on `\(.workspace)` has finished"
  elif .status == "planned" then "Planned `\(.workspace)`: nothing was applied"
  elif .status == "skipped" then "Skipped `\(.workspace)`"
  else "\(.mention // "")A run on `\(.workspace)` has failed" end;
def detail:
  if (.status | IN("applied", "planned", "in-sync")) then null
  elif (.summary // "") != "" then (.summary | bt | cut(300)) else null end;
if .status == "no-changes" and .kind != "unlock" then empty
else
  # (parentheses around the whole value: jq 1.7 does not accept an unparenthesised `+` in an object value)
  {text: (([head, by, logs, detail] | map(select(. != null)) | join("\n")) + block)}
end
