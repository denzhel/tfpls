#!/usr/bin/env bash
# What an issue-agent run used: one line in the run summary and one comment on the issue. The comment carries the marker
# that the gate counts for the per-issue cap (tools/agent-gate.sh), and it is posted for a failed run too (it cost something).
# Its last line says what happens next, from the outcome and the labels the agent left (every agent comment ends with one).
#   tools/agent-report.sh
# Inputs (environment): PHASE (elaborate | implement), MODE (elaborate | proposal, from the gate; optional), ISSUE, REPO, RUN_URL,
# OUTCOME (the agent step's outcome), EXEC (the execution file of anthropics/claude-code-action: JSON or JSON lines, with a final
# "result" message), GH_TOKEN.
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
if [ "$res" = null ]; then
  # no result record at all: the agent did not run (a green job can hide that). The Claude action skips itself, with a warning in its
  # log, when the workflow file of the run differs from the one on the default branch (a security check that also hits a manual run from
  # a branch that changed the workflow file) or when it cannot start.
  line="Agent run (${PHASE}): ${outcome}, **the agent produced no result** (no execution record; the Claude action may have skipped itself, see the run log)"
fi
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

# what happens next: from the outcome and the labels after the run
labels=$(gh issue view "$ISSUE" --repo "$REPO" --json labels --jq '[.labels[].name] | join(",")' 2>/dev/null || true)
has_label() { case ",$labels," in *",$1,"*) return 0;; esac; return 1; }
if [ "$PHASE" = implement ]; then
  if [ -n "${pr:-}" ]; then next="**Next:** the owner reviews the pull request; the issue is closed when its evidence is in."
  else next="**Next:** read the agent's last words and the run log; the owner can comment \`/implement\` with instructions to try again."; fi
elif [ "$res" = null ] || [ "$outcome" != success ]; then
  next="**Next:** the run did not finish (see the run log): comment \`/elaborate\` to try again, or fill in the issue by hand."
elif [ "${MODE:-}" = proposal ]; then
  next="**Next:** a maintainer takes what fits from the proposal above (the issue body is unchanged); \`/elaborate\` with corrections updates the proposal."
elif has_label needs-info; then
  next="**Next:** answer the questions above in a comment (a plain \"yes\" accepts the defaults); any comment of the owner or a collaborator runs the agent again."
elif has_label ready; then
  next="**Next:** the owner comments \`/implement\` (optionally with instructions) to start the implementation; to change an assumption first, comment \`/elaborate\` with the change."
else
  next="**Next:** the agent left no label: comment \`/elaborate\` to run it again, or label the issue \`ready\` by hand when the body is complete."
fi

body="$line${extra:+
$extra}

$next"
echo "$body"
[ -n "${GITHUB_STEP_SUMMARY:-}" ] && echo "$body" >> "$GITHUB_STEP_SUMMARY"
gh issue comment "$ISSUE" --repo "$REPO" --body "$body
<!-- tfpls-agent-run: ${PHASE} -->" >/dev/null 2>&1 || echo "::warning::could not post the agent run report"
exit 0
