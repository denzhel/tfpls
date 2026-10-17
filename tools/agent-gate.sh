#!/usr/bin/env bash
# The gate of the issue agent: decides BEFORE any model runs whether a run is needed (cost control), and prepares a
# trusted copy of the issue for the agent. Used by .github/workflows/claude-elaborate.yml and claude-implement.yml.
#   tools/agent-gate.sh elaborate|implement
# Inputs (environment): REPO, ISSUE, EVENT_NAME (issues | issue_comment), SENDER_TYPE, ACTOR_ASSOC (the commenter's
# author_association), COMMENT_BODY, GH_TOKEN, and the optional limits ENABLED, MAX_ELAB_ISSUE, MAX_IMPL_ISSUE,
# MAX_ELAB_DAY, MAX_IMPL_DAY. Outputs (GITHUB_OUTPUT): run=true|false, reason, issue_file, note_file, comment_file.
# Issue and comment text is data: only the owner's and collaborators' words (and our own bots') reach the agent.
set -uo pipefail

phase=${1:-}
case "$phase" in elaborate|implement) ;; *) echo "usage: tools/agent-gate.sh elaborate|implement" >&2; exit 2;; esac
REPO=${REPO:?}; ISSUE=${ISSUE:?}
OUT=${GITHUB_OUTPUT:-/dev/null}
WORK=${AGENT_WORK:-${RUNNER_TEMP:-/tmp}}
now=${TFPLS_NOW:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
mkdir -p "$WORK"

skip() { # reason [text to post on the issue]
  echo "gate: no run ($1)"
  { echo "run=false"; echo "reason=$1"; } >> "$OUT"
  if [ -n "${2:-}" ]; then printf '%s\n' "$2" > "$WORK/gate-comment.md"; echo "comment_file=$WORK/gate-comment.md" >> "$OUT"; fi
  exit 0
}
trusted() { case "$1" in OWNER|MEMBER|COLLABORATOR) return 0;; esac; return 1; }

# ---- cheap checks first: nothing below costs a model run
[ "${ENABLED:-true}" = false ] && skip "the agent is switched off (repository variable TFPLS_AGENT_ENABLED=false)"
[ "${SENDER_TYPE:-User}" = Bot ] && skip "the event was caused by a bot"

issue_json=$(gh api "repos/$REPO/issues/$ISSUE") || skip "could not read the issue"
jq -e '.pull_request' <<<"$issue_json" >/dev/null 2>&1 && skip "it is a pull request"
labels=$(jq -r '[.labels[].name] | join(",")' <<<"$issue_json")
has_label() { case ",$labels," in *",$1,"*) return 0;; esac; return 1; }
author_assoc=$(jq -r '.author_association // "NONE"' <<<"$issue_json")
body=$(jq -r '.body // ""' <<<"$issue_json")

# a body counts as elaborated when the template sections are there and at least one task and one criterion has text
elaborated() {
  local b=$1
  grep -qiE '^#+[[:space:]]+Outcome' <<<"$b" && grep -qiE '^#+[[:space:]]+Tasks' <<<"$b" \
    && grep -qiE '^#+[[:space:]]+Acceptance criteria' <<<"$b" && grep -qiE '^#+[[:space:]]+Test criteria' <<<"$b" \
    && [ "$(grep -cE '^[[:space:]]*- \[[ xX]\] +[^[:space:]]' <<<"$b")" -ge 2 ]
}

# ---- who and what may start a run
comment_first=$(printf '%s' "${COMMENT_BODY:-}" | head -1 | sed -E 's/^[[:space:]]+//')
case "$phase" in
  elaborate)
    if [ "${EVENT_NAME:-}" = issues ]; then
      [ "$author_assoc" = OWNER ] || skip "the issue was not opened by the owner"
      elaborated "$body" && skip "the issue is already elaborated"
    else
      has_label needs-info || skip "the issue is not waiting for answers"
      trusted "${ACTOR_ASSOC:-NONE}" || skip "the comment is not from the owner or a collaborator"
      case "$comment_first" in /*) skip "the comment is a command, not an answer";; esac
    fi;;
  implement)
    [ "${EVENT_NAME:-}" = issue_comment ] || skip "not a comment"
    case "$comment_first" in /implement|"/implement "*) ;; *) skip "the comment is not /implement";; esac
    [ "${ACTOR_ASSOC:-NONE}" = OWNER ] || skip "/implement is for the owner" "/implement was ignored: only the repository owner can start an implementation."
    trusted "$author_assoc" || skip "the issue was written by someone who is not the owner or a collaborator" "/implement was ignored: the issue was not written by the owner or a collaborator."
    has_label needs-info && skip "the issue still has open questions" "/implement was ignored: the issue still has open questions (label \`needs-info\`). Answer them first."
    has_label ready || skip "the issue is not ready" "/implement was ignored: the issue is not labelled \`ready\` (the elaboration labels it when it has no open questions)."
    ;;
esac

# ---- run caps (per issue and per 24 hours): an agent run must never be unbounded
comments_json=$(gh api --paginate "repos/$REPO/issues/$ISSUE/comments?per_page=100" 2>/dev/null | jq -s 'add // []') || comments_json='[]'
marker="tfpls-agent-run: $phase"
per_issue=$(jq -r --arg m "$marker" '[.[] | select(.user.type == "Bot" and ((.body // "") | contains($m)))] | length' <<<"$comments_json")
since=$(jq -rn --arg n "$now" '($n | fromdateiso8601) - 86400 | todateiso8601')
runs_today() { # workflow file: agent jobs started in the last 24 hours (a skipped agent job costs nothing)
  local id n=0 c
  for id in $(gh api -X GET "repos/$REPO/actions/workflows/$1/runs" -f created=">=$since" -f per_page=50 --jq '.workflow_runs[].id' 2>/dev/null); do
    c=$(gh api "repos/$REPO/actions/runs/$id/jobs" --jq '[.jobs[] | select(.name == "agent" and .conclusion != "skipped")] | length' 2>/dev/null || echo 0)
    n=$((n + c))
  done
  echo "$n"
}
if [ "$phase" = elaborate ]; then
  max_i=${MAX_ELAB_ISSUE:-3}; max_d=${MAX_ELAB_DAY:-20}; wf=claude-elaborate.yml
else
  max_i=${MAX_IMPL_ISSUE:-2}; max_d=${MAX_IMPL_DAY:-5}; wf=claude-implement.yml
fi
[ "$per_issue" -ge "$max_i" ] && skip "the per-issue cap is reached ($per_issue of $max_i $phase runs)" "The agent already ran $per_issue times ($phase) on this issue; the limit is $max_i. Please continue by hand or raise \`MAX_*\` in the repository variables."
today=$(runs_today "$wf")
[ "$today" -ge "$max_d" ] && skip "the daily cap is reached ($today of $max_d $phase runs in 24 hours)" "The daily limit of agent runs ($max_d $phase runs in 24 hours) is reached; try again later."

# ---- the trusted copy of the issue for the agent (it never calls `gh issue view` itself)
f="$WORK/issue.md"
{
  jq -r '"# Issue #\(.number): \(.title)\n\nLabels: \([.labels[].name] | join(", "))\nAuthor: \(.user.login) (\(.author_association))\n\n## Body\n"' <<<"$issue_json"
  if trusted "$author_assoc"; then printf '%s\n' "$body"; else echo "[the body is not included: it was not written by the owner or a collaborator]"; fi
  printf '\n## Comments (only the owner, collaborators and our own bots)\n'
  jq -r '.[] | select(((.author_association == "OWNER") or (.author_association == "MEMBER") or (.author_association == "COLLABORATOR"))
                       or (.user.type == "Bot" and (.user.login == "claude" or .user.login == "github-actions" or .user.login == "claude[bot]" or .user.login == "github-actions[bot]")))
        | "\n### @\(.user.login) (\(.author_association)) at \(.created_at)\n\(.body)"' <<<"$comments_json"
} > "$f"
{ echo "run=true"; echo "reason=ok"; echo "issue_file=$f"; } >> "$OUT"

# the owner's own words after /implement are instructions for this run
if [ "$phase" = implement ]; then
  note=$(printf '%s' "${COMMENT_BODY:-}" | sed -E '1s#^[[:space:]]*/implement[[:space:]]*##')
  if [ -n "$(printf '%s' "$note" | tr -d '[:space:]')" ]; then printf '%s\n' "$note" > "$WORK/implement-note.md"; echo "note_file=$WORK/implement-note.md" >> "$OUT"; fi
fi
echo "gate: run ($phase #$ISSUE; $per_issue of $max_i on this issue, $today of $max_d in 24 hours)"
