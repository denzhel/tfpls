#!/usr/bin/env bash
# What an issue-agent run used: one line in the run summary and one comment on the issue. The comment carries the marker
# that the gate counts for the per-issue cap (tools/agent-gate.sh), and it is posted for a failed run too (it cost something).
#   tools/agent-report.sh
# Inputs (environment): PHASE (elaborate | implement), ISSUE, REPO, RUN_URL, OUTCOME (the agent step's outcome), EXEC (the
# execution file of anthropics/claude-code-action: JSON or JSON lines, with a final "result" message), GH_TOKEN.
# Best effort: it never fails the job.
set -uo pipefail
PHASE=${PHASE:?}; ISSUE=${ISSUE:?}; REPO=${REPO:?}
res='null'
if [ -n "${EXEC:-}" ] && [ -f "$EXEC" ]; then
  res=$(jq -s -c 'flatten | map(select(type == "object" and .type == "result")) | last // null' "$EXEC" 2>/dev/null || echo null)
fi
turns=$(jq -r '.num_turns // "?"' <<<"$res" 2>/dev/null || echo "?")
secs=$(jq -r 'if .duration_ms then ((.duration_ms / 1000) | round) else "?" end' <<<"$res" 2>/dev/null || echo "?")
cost=$(jq -r 'if .total_cost_usd then (.total_cost_usd * 100 | round / 100 | tostring) else "not reported" end' <<<"$res" 2>/dev/null || echo "not reported")
outcome=${OUTCOME:-unknown}
line="Agent run (${PHASE}): ${outcome}, ${turns} turns, ${secs} s, cost ${cost}$([ "$cost" != "not reported" ] && echo " USD")"
[ -n "${RUN_URL:-}" ] && line="$line ([run](${RUN_URL}))"

# what went wrong or was left undone, so that a "success" without a result is never silent: the tool calls that were denied
# (a tool list that is too narrow), the agent's last words, and for an implementation whether a pull request exists
extra=""
denied=$(jq -r '(.permission_denials // []) | .[:8][] | "- `\(.tool_name)`: \((.tool_input.command // .tool_input.file_path // (.tool_input | tostring)) | gsub("[\r\n]+"; " ") | .[0:110])"' <<<"$res" 2>/dev/null || true)
ndenied=$(jq -r '(.permission_denials // []) | length' <<<"$res" 2>/dev/null || echo 0)
if [ "${ndenied:-0}" -gt 0 ] 2>/dev/null; then
  extra="${extra}
**${ndenied} tool call(s) were denied** (not in the tool list):
${denied}"
fi
if [ "$PHASE" = implement ]; then
  pr=$(gh pr list --repo "$REPO" --head "agent/issue-${ISSUE}" --state all --json number,url --jq '.[0] | if . then "pull request #\(.number): \(.url)" else empty end' 2>/dev/null || true)
  if [ -n "$pr" ]; then extra="${extra}
Result: ${pr}"; else extra="${extra}
**No pull request was opened for this run.**"; fi
fi
last=$(jq -r '.result // empty' <<<"$res" 2>/dev/null | tail -c 900 || true)
[ -n "$last" ] && extra="${extra}
<details><summary>The agent's last words</summary>

${last}

</details>"

body="$line${extra:+
$extra}"
echo "$body"
[ -n "${GITHUB_STEP_SUMMARY:-}" ] && echo "$body" >> "$GITHUB_STEP_SUMMARY"
gh issue comment "$ISSUE" --repo "$REPO" --body "$body
<!-- tfpls-agent-run: ${PHASE} -->" >/dev/null 2>&1 || echo "::warning::could not post the agent run report"
exit 0
