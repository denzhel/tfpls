#!/usr/bin/env bash
# Parse and guard a drift fix: a comment `/tofu plan|apply|fix [-target=addr ...]` on an open issue titled `Drift: <workspace>`,
# or a manual run (task `fix`: WORKSPACES names, STEP plan|apply|fix).
# Env: EVENT (issue_comment | workflow_dispatch), BODY, ACTOR, ISSUE_NUMBER, ISSUE_TITLE, ISSUE_STATE, IS_PR, COMMENT_ID,
#      IN_WORKSPACES, IN_STEP, REPO, DEFAULT_BRANCH, GH_TOKEN.  Outputs (GITHUB_OUTPUT): ok, cmd, names, targets, sha, issue.
# A refusal is explained on the issue; a problem with the person or the arguments fails the job, an unrelated issue is ignored.
set -uo pipefail
set -f   # comment words are never glob-expanded
out() { echo "$1=$2" >> "${GITHUB_OUTPUT:-/dev/null}"; }
issue="${ISSUE_NUMBER:-}"
clean() { local m=${1//\`/}; printf '%s' "$m" | sed -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }
reply() { [ -n "$issue" ] && gh issue comment "$issue" --repo "$REPO" --body "$1" >/dev/null 2>&1 || true; }
react() { [ -n "${COMMENT_ID:-}" ] && gh api -X POST "repos/$REPO/issues/comments/$COMMENT_ID/reactions" -f content="$1" >/dev/null 2>&1 || true; }
fail()   { echo "::error::$1"; reply "❌ $(clean "$1")"; react confused; out ok false; exit 1; }
ignore() { echo "$1"; reply "ℹ️ $(clean "$1")"; react confused; out ok false; exit 0; }

[ "$EVENT" != issue_comment ] || [ "${IS_PR:-false}" != true ] || { out ok false; exit 0; }   # pull requests are handled by comment-ops
perm=$(gh api "repos/$REPO/collaborators/$ACTOR/permission" --jq .permission 2>/dev/null || echo none)
[[ "$perm" == admin || "$perm" == write || "$perm" == maintain ]] || fail "$ACTOR lacks write access"

cmd=""; names=""; targets=""
if [ "$EVENT" = issue_comment ]; then
  [[ "${ISSUE_TITLE:-}" =~ ^Drift:\ ([A-Za-z0-9_./-]+)$ ]] || ignore "/tofu commands on an issue work only on a \`Drift: <workspace>\` issue (comment \`/tofu plan\`, \`/tofu apply\` or \`/tofu fix\` there); to change code open a pull request"
  names="${BASH_REMATCH[1]}"
  # the drift issue is made by this pipeline (a bot) and carries its label: a stranger's issue with the same title is not one
  [ "${ISSUE_USER_TYPE:-}" = Bot ] && jq -e 'index("tfpls-drift")' <<<"${ISSUE_LABELS:-[]}" >/dev/null 2>&1 \
    || ignore "this is not a drift issue made by the pipeline (it must be opened by the bot and carry the \`tfpls-drift\` label)"
  [ "${ISSUE_STATE:-open}" = open ] || ignore "this drift issue is closed: the workspace is back in sync"
  react eyes
  read -r _ cmd rest <<<"$(head -n1 <<<"${BODY:-}" | tr -d '\r')"
  [[ "$cmd" == plan || "$cmd" == apply || "$cmd" == fix ]] || fail "unknown command: $cmd (use plan, apply or fix)"
  for tok in $rest; do
    if [[ "$tok" == -target=* && "${tok#-target=}" =~ ^[][A-Za-z0-9_.\"\'-]+$ ]]; then targets+="${tok#-target=} "
    else fail "unexpected argument: $tok (a drift issue is one workspace: only -target=<address> is allowed)"; fi
  done
else
  cmd="${IN_STEP:-fix}"; [ -n "$cmd" ] || cmd=fix
  [[ "$cmd" == plan || "$cmd" == apply || "$cmd" == fix ]] || fail "unknown step: $cmd (use plan, apply or fix)"
  for tok in ${IN_WORKSPACES//,/ }; do
    [[ "$tok" =~ ^[A-Za-z0-9_./-]+$ ]] || fail "unexpected workspace name: $tok (exact names only, no globs)"
    names+="$tok "
  done
  names="${names% }"
  [ -n "$names" ] || fail "name the workspace(s) to fix: the input workspaces"
fi
sha=$(gh api "repos/$REPO/commits/${DEFAULT_BRANCH:?}" --jq .sha 2>/dev/null) || fail "could not read the default branch $DEFAULT_BRANCH"
{ echo "ok=true"; echo "cmd=$cmd"; echo "names=$names"; echo "targets=${targets% }"; echo "sha=$sha"; echo "issue=$issue"; } >> "${GITHUB_OUTPUT:-/dev/null}"
echo "drift fix: $cmd on $names from $sha${targets:+ (targets: ${targets% })}"
