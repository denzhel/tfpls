#!/usr/bin/env bash
# Fetch CODEOWNERS from the DEFAULT branch tip (a pull request must not be able to change who has to approve it), read the
# reviews (only people with write access count), and run check.sh for the workspace paths of the entries (rules from the
# default branch plus the pull request's own). A refusal is explained on the pull request.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
co=$(mktemp); found=""
# only a 404 means "no such file"; any other failure (API error, rate limit) must not read as "nothing to enforce"
for f in .github/CODEOWNERS CODEOWNERS docs/CODEOWNERS; do
  if err=$(gh api "repos/$REPO/contents/$f?ref=$BASE" -H "Accept: application/vnd.github.raw" 2>&1 > "$co"); then found=1; break; fi
  case $err in *404*|*"Not Found"*) ;; *)
    echo "::error::could not read $f on the base branch: $err"
    gh pr comment "$PR" --repo "$REPO" --body "⛔ **apply refused.** Could not read the CODEOWNERS file on the default branch (a GitHub API error, not a missing file). Try again in a minute." >/dev/null || true
    exit 1;;
  esac
done
[ -n "$found" ] || { echo "no CODEOWNERS file on the base branch: nothing to enforce"; exit 0; }
reviews=$(mktemp)
"$here/../approvals/reviews.sh" "$REPO" "$PR" > "$reviews" || echo '[]' > "$reviews"
paths=(); while IFS= read -r p; do paths+=("$p"); done < <(jq -r '.[].owner_paths' <<<"$ENTRIES" | sed '/^$/d' | sort -u)
if out=$("$here/check.sh" "$co" "$reviews" "$SHA" "${paths[@]}" 2>&1); then echo "$out"; exit 0; fi
echo "::error::$out"
gh pr comment "$PR" --repo "$REPO" --body "⛔ **apply refused.** $out" >/dev/null || true
[ -n "${COMMENT_ID:-}" ] && gh api -X POST "repos/$REPO/issues/comments/$COMMENT_ID/reactions" -f content=-1 >/dev/null 2>&1
exit 1
