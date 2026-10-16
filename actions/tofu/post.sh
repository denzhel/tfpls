#!/usr/bin/env bash
# Posting the plan comment of a workspace (sourced by run.sh and the tests).

# tfpls_post_comment <marker> <body>
# Every run posts a NEW comment; the newest one is the live one. The bot's older comments with the same marker keep their
# text and get a one-line banner that links the new comment (the marker stays on the first line). The new comment is posted
# first, so a failure never leaves the pull request without a plan. Needs GITHUB_REPOSITORY and PR.
# Only the bot's own comments are touched: a stranger's comment with the marker is never edited.
tfpls_post_comment() {
  local marker=$1 body=$2 old new url line id banner text
  old=$(gh api "repos/$GITHUB_REPOSITORY/issues/$PR/comments?per_page=100" --paginate 2>/dev/null \
        | jq -s -c --arg m "$marker" 'add // [] | .[]
            | select(.user.type == "Bot" and (.body | startswith($m)) and ((.body | split("\n")[1] // "") | startswith("> 🔁 **Superseded**") | not))
            | {id, body}') || old=""
  if ! new=$(gh api "repos/$GITHUB_REPOSITORY/issues/$PR/comments" -f body="$body"); then
    echo "::error::could not post the plan comment (does the workflow grant pull-requests: write?)"
    return 0
  fi
  url=$(jq -r '.html_url // empty' <<<"$new" 2>/dev/null)
  [ -n "$old" ] && [ -n "$url" ] || return 0
  banner="> 🔁 **Superseded** by $url. The plan below is kept for reference only."
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    id=$(jq -r .id <<<"$line")
    text=$(jq -r --arg b "$banner" '.body | split("\n") | .[0] + "\n" + $b + "\n" + (.[1:] | join("\n"))' <<<"$line")
    gh api -X PATCH "repos/$GITHUB_REPOSITORY/issues/comments/$id" -f body="$text" >/dev/null 2>&1 \
      || echo "::warning::could not mark the older plan comment $id as superseded"
  done <<<"$old"
  return 0
}
