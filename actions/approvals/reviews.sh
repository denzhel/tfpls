#!/usr/bin/env bash
# The reviews of a pull request that may count: only people with write access to the repository.
# usage: reviews.sh <repo> <pr>   -> prints a JSON array of reviews on stdout; who was left out goes to $IGNORED_OUT ("login (reason)" per line)
# A review counts only when its author is a user (not a bot, not deleted) whose permission on the repository is admin or write
# (the API reports maintain as write and triage as read). A failed lookup means the review is not counted.
# Test override: TFPLS_REVIEWS_FILE (instead of the API).
set -uo pipefail
repo=${1:?repo}; pr=${2:?pr}
all=$(mktemp); trap 'rm -f "$all"' EXIT
if [ -n "${TFPLS_REVIEWS_FILE:-}" ]; then cp "$TFPLS_REVIEWS_FILE" "$all"
else gh api --paginate "repos/$repo/pulls/$pr/reviews" | jq -s 'add // []' > "$all"; fi

ok_json='[]'; ignored=""
for login in $(jq -r '[.[] | select(.state == "APPROVED" or .state == "CHANGES_REQUESTED" or .state == "DISMISSED") | select(.user != null and .user.type == "User") | .user.login] | unique[]' "$all"); do
  if ! [[ "$login" =~ ^[A-Za-z0-9][A-Za-z0-9_-]*$ ]]; then ignored="$ignored$login (unusual login)"$'\n'; continue; fi
  if perm=$(gh api "repos/$repo/collaborators/$login/permission" --jq .permission 2>/dev/null) && [ -n "$perm" ]; then
    case "$perm" in admin|write) ok_json=$(jq -c --arg l "$login" '. + [$l]' <<<"$ok_json");; *) ignored="$ignored$login (no write access)"$'\n';; esac
  else ignored="$ignored$login (permission could not be checked)"$'\n'; fi
done
[ -z "${IGNORED_OUT:-}" ] || printf '%s' "$ignored" > "$IGNORED_OUT"
jq -c --argjson ok "$ok_json" '[.[] | select(.user != null and .user.type == "User") | select(.user.login as $l | $ok | index($l))]' "$all"
