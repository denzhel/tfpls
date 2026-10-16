#!/usr/bin/env bash
# Approval policy for /tofu apply, per workspace rule (see docs/approvals.md).
# Env: ENTRIES (the RULES from rules.sh: approvals_mode and approvals_required per workspace, taken from the default branch),
#      SHA, PR, COMMENT_ID, REPO, GH_TOKEN, DEFAULT_BRANCH (name, for the message).
# Only reviews of people with write access count (reviews.sh). Test overrides: TFPLS_REVIEWS_FILE, TFPLS_DRY (print the refusal instead of commenting on the PR).
# Every workspace's rule must be satisfied (the strictest one effectively wins). Exit 1 = refused (explained on the PR).
set -uo pipefail
DEFAULT_BRANCH_NAME=${DEFAULT_BRANCH:-the default branch}
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# no rules (empty, invalid or not a list) must never read as "nothing is required"
jq -e 'type == "array" and length > 0' <<<"${ENTRIES:-}" >/dev/null 2>&1 || { echo "::error::no approval rules to evaluate"; exit 1; }
reviews=$(mktemp); ignored=$(mktemp); trap 'rm -f "$reviews" "$ignored"' EXIT
# no review is needed anywhere (required: 0): do not look anybody up
if jq -e 'all(.[]; .approvals_required == "0")' <<<"$ENTRIES" >/dev/null 2>&1; then echo '[]' > "$reviews"
else IGNORED_OUT="$ignored" "$here/reviews.sh" "$REPO" "$PR" > "$reviews" || echo '[]' > "$reviews"; fi

# the repository requires approval of the most recent push by somebody else: the author and committer of the head commit do not count
if [ "${LAST_PUSH:-}" = true ]; then
  pushers=${TFPLS_COMMIT_LOGINS:-$(gh api "repos/$REPO/commits/$SHA" --jq '[.author.login, .committer.login] | map(select(. != null)) | unique | join(" ")' 2>/dev/null)}
  for who in $pushers; do
    if jq -e --arg l "$who" 'any(.[]; .user.login == $l and .state == "APPROVED")' "$reviews" >/dev/null 2>&1; then
      echo "$who (pushed the latest commit, which the repository does not allow to approve it)" >> "$ignored"
      jq -c --arg l "$who" 'map(select(.user.login != $l))' "$reviews" > "$reviews.new" && mv "$reviews.new" "$reviews"
    fi
  done
fi

refuse() {
  local note=""
  if [ -s "$ignored" ]; then note=" Not counted: $(sed 's/$/, /' "$ignored" | tr -d '\n' | sed 's/, $//')."; fi
  note="$note The number of approvals comes from the repository's ruleset for \`$DEFAULT_BRANCH_NAME\` (Settings, Rules; 1 when there is none). In an emergency a repository admin can comment \`/tofu apply <workspace> --now\`."
  local msg="⛔ **apply refused.** $1$note"
  echo "::error::$1"
  if [ -n "${TFPLS_DRY:-}" ]; then echo "$msg"; exit 1; fi
  gh pr comment "$PR" --repo "$REPO" --body "$msg" >/dev/null || true
  [ -n "${COMMENT_ID:-}" ] && gh api -X POST "repos/$REPO/issues/comments/$COMMENT_ID/reactions" -f content=-1 >/dev/null 2>&1
  exit 1
}

while IFS=$'\x1f' read -r name md rq; do   # unit separator: empty fields must survive (tab is IFS-whitespace and would collapse them)
  [ -n "$md" ] || md=strict
  [ -n "$rq" ] || rq=1
  if out=$("$here/check.sh" "$reviews" "$SHA" "$md" "$rq" 2>&1); then
    echo "$name: approvals ok (mode $md, $rq required)"
  else
    refuse "\`$name\`: $out  _(mode \`$md\`, $rq required)_"
  fi
done < <(jq -r '.[] | [.name, (.approvals_mode // ""), (.approvals_required // "")] | join("\u001f")' <<<"$ENTRIES")
exit 0
