#!/usr/bin/env bash
# One sticky note on the pull request for the two first-run situations that otherwise end in a green check and no comment.
#   introduced      the caller workflow does not exist on the base commit: this pull request ADDS the pipeline, so every workspace is
#                   planned once (output introduced=true|false) and the note says why /tofu comments do not answer yet
#   no-workspaces   the config (or the default layout) describes no workspace: say how to describe the layout
#   clear           remove the no-workspaces note when workspaces are found again
# Env: BASE_SHA, WF_REF (github.workflow_ref), REPO, PR, GH_TOKEN, CONFIG, GITHUB_OUTPUT, GITHUB_ACTION_REPOSITORY
# Only comments of the bot are updated or removed (a stranger's comment with the same first line is never touched). Best effort: never fails the job.
set -uo pipefail
mode=${1:?mode}
repo_docs="https://github.com/${GITHUB_ACTION_REPOSITORY:-denzhel/tfpls}/blob/main"
upsert() { # <marker> <body>
  local marker=$1 body="$1
$2" id
  [ -n "${PR:-}" ] && [ -n "${REPO:-}" ] || return 0
  id=$(gh api --paginate "repos/$REPO/issues/$PR/comments?per_page=100" 2>/dev/null | jq -s -r --arg m "$marker" 'add // [] | .[] | select(.user.type == "Bot" and (.body | startswith($m))) | .id' | head -1)
  if [ -n "$id" ]; then gh api -X PATCH "repos/$REPO/issues/comments/$id" -f body="$body" >/dev/null 2>&1 || true
  else gh api "repos/$REPO/issues/$PR/comments" -f body="$body" >/dev/null 2>&1 || true; fi
}
case "$mode" in
  introduced)
    introduced=false
    path=${WF_REF#"$REPO"/}; path=${path%%@*}
    if [ -n "${BASE_SHA:-}" ] && [ -n "$path" ] && git cat-file -e "${BASE_SHA}^{commit}" 2>/dev/null && ! git cat-file -e "${BASE_SHA}:${path}" 2>/dev/null; then introduced=true; fi
    [ -z "${GITHUB_OUTPUT:-}" ] || echo "introduced=$introduced" >> "$GITHUB_OUTPUT"
    if [ "$introduced" = true ]; then
      upsert "<!-- tfpls:introduced -->" "ℹ️ This pull request adds the pipeline (\`$path\`), so **every workspace is planned once**. \`/tofu\` comments are answered after it is merged into the default branch: GitHub runs comment workflows from there."
    fi;;
  no-workspaces)
    upsert "<!-- tfpls:no-workspaces -->" "ℹ️ No workspaces were found. The default layout is one folder per workspace under \`workspaces/\`. Describe yours in \`${CONFIG:-.tfpls.yml}\`, for example separate root modules \`stacks: [{ name: dev, dir: envs/dev, workspaces: [default], watch: [modules] }, ...]\`, or one root module with var files \`stacks: [{ name: app, dir: ., workspaces: [dev, prod], var_file: \"vars/{workspace}.tfvars\", workspace_paths: [\"vars/{workspace}.tfvars\"] }]\`. More: $repo_docs/docs/getting-started.md#another-layout. This comment disappears once workspaces are found.";;
  clear)
    [ -n "${PR:-}" ] && [ -n "${REPO:-}" ] || exit 0
    gh api --paginate "repos/$REPO/issues/$PR/comments?per_page=100" 2>/dev/null | jq -s -r 'add // [] | .[] | select(.user.type == "Bot" and (.body | startswith("<!-- tfpls:no-workspaces -->"))) | .id' \
      | while read -r id; do [ -n "$id" ] && gh api -X DELETE "repos/$REPO/issues/comments/$id" >/dev/null 2>&1; done; true;;
  *) echo "usage: notes.sh introduced|no-workspaces|clear" >&2; exit 2;;
esac
exit 0
