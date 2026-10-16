#!/usr/bin/env bash
# For the newest plan comment (<!-- tfpls:NAME -->) of every workspace that is NOT in NAMES (a JSON array of names), add a banner saying
# that the latest commit does not affect it and the plan is from an earlier commit. Idempotent. Best-effort.
set -uo pipefail
REPO="${GITHUB_REPOSITORY:?}"
selected=$(jq -c 'if type == "array" then . else [] end' <<<"${NAMES:-[]}" 2>/dev/null || echo '[]')
short=$(printf '%s' "${SHA:-}" | cut -c1-7)
banner="> ⏭️ **Outdated:** this workspace is not affected by the latest commit (\`$short\`). The plan below is from an earlier commit and is kept for reference only."

comments=$(gh api --paginate "repos/$REPO/issues/$PR/comments?per_page=100" | jq -s 'add // []') || { echo "::warning::could not list comments"; exit 0; }
jq -c --argjson sel "$selected" '
  [ .[] | select(.user.type == "Bot" and (.body | startswith("<!-- tfpls:")))
        | {id, body, name: (.body | capture("^<!-- tfpls:(?<n>[^ ]+) -->").n)}
        | select(.name != "introduced" and .name != "no-workspaces") ]   # notes of the pipeline, not a workspace plan
  | group_by(.name) | map(max_by(.id)) | .[]   # only the newest comment of a workspace; the older ones are superseded
      | select(.name as $n | ($sel | index($n)) == null)
      | select(.body | contains("is not affected by the latest commit") | not)' <<<"$comments" |
while IFS= read -r c; do
  id=$(jq -r .id <<<"$c")
  new=$(jq -r --arg b "$banner" '.body | split("\n") | .[0] + "\n" + $b + "\n" + (.[1:] | join("\n"))' <<<"$c")
  gh api -X PATCH "repos/$REPO/issues/comments/$id" -f body="$new" >/dev/null \
    && echo "marked outdated: $(jq -r .name <<<"$c")" || echo "::warning::could not update comment $id"
done
exit 0
