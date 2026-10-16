#!/usr/bin/env bash
# Approval rules of a pull request come from the DEFAULT BRANCH, never from the pull request's own config.
# Env: HEAD_ENTRIES (detect-workspaces entries of the PR), CONFIG (path of the config file), DEFAULT_BRANCH, REPO, GH_TOKEN,
#      GITHUB_OUTPUT (rules, entries, owner_entries) or TFPLS_DRY (print the result).
# Test override: TFPLS_BASE_CONFIG_FILE (a YAML file, or the word "missing").
# The config on the default branch is read from the API at its tip (a stale base commit or another base branch cannot weaken it).
# Missing file -> defaults. Any other failure, or a config with errors -> refuse (exit 1).
# HOW MANY approvals, strict or not, CODEOWNERS and the last-push rule come from the repository's own settings: the active
# rulesets of the default branch (pull_request rule). No such rule -> 1 approval of the current commit (the safe default).
# Test override: TFPLS_RULESET_FILE (the JSON the rules API returns).
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
refuse() { echo "::error::$1"; [ -n "${TFPLS_DRY:-}" ] && { echo "$1"; exit 1; }
  [ -n "${PR:-}" ] && gh pr comment "$PR" --repo "$REPO" --body "⛔ **apply refused.** $1" >/dev/null 2>&1 || true
  [ -n "${COMMENT_ID:-}" ] && gh api -X POST "repos/$REPO/issues/comments/$COMMENT_ID/reactions" -f content=-1 >/dev/null 2>&1 || true
  exit 1; }

raw=$(mktemp); trap 'rm -f "$raw"' EXIT
base_json='{}'
if [ -n "${TFPLS_BASE_CONFIG_FILE:-}" ]; then
  if [ "$TFPLS_BASE_CONFIG_FILE" != missing ]; then cp "$TFPLS_BASE_CONFIG_FILE" "$raw"; have=1; else have=0; fi
else
  have=0
  if out=$(gh api "repos/$REPO/contents/$CONFIG?ref=$DEFAULT_BRANCH" 2>&1); then
    [ "$(jq -r '.type // ""' <<<"$out")" = file ] || refuse "the config \`$CONFIG\` on the default branch (\`$DEFAULT_BRANCH\`) is not a regular file."
    jq -r '.content' <<<"$out" | base64 -d > "$raw" 2>/dev/null || refuse "could not decode \`$CONFIG\` on the default branch."
    have=1
  elif grep -q "HTTP 404" <<<"$out"; then have=0
  else refuse "could not read \`$CONFIG\` on the default branch (\`$DEFAULT_BRANCH\`): approval rules cannot be checked."; fi
fi
if [ "$have" = 1 ]; then
  base_json=$(yq -o=json '.' "$raw" 2>/dev/null) || refuse "the config on the default branch is not valid YAML; merge a fix there before \`/tofu apply\` can check approvals."
  [ -n "$base_json" ] && [ "$base_json" != null ] || base_json='{}'
  problems=$(jq -c -f "$here/../detect-workspaces/validate.jq" <<<"$base_json")
  if jq -e 'any(.[]; .level == "error")' <<<"$problems" >/dev/null; then
    refuse "the config on the default branch has errors ($(jq -r '[.[]|select(.level=="error")|.msg][0]' <<<"$problems")); merge a fix there before \`/tofu apply\` can check approvals."
  fi
fi
# detect-workspaces gives an OBJECT keyed by label; policy.jq works on a list, and `entries` goes back out as the same kind of object
heads=$(jq -c 'if type == "object" then [.[]] else . end' <<<"$HEAD_ENTRIES") || refuse "could not read the pull request's workspaces."
result=$(jq -c --argjson base "$base_json" -f "$here/policy.jq" <<<"$heads") || refuse "could not work out the approval rules."
if [ -n "${TFPLS_RULESET_FILE:-}" ]; then rs=$(cat "$TFPLS_RULESET_FILE")
else rs=$(gh api --paginate "repos/$REPO/rules/branches/$DEFAULT_BRANCH?per_page=100" 2>/dev/null | jq -s 'add // []') || rs=""; fi
[ -n "$rs" ] && jq -e 'type == "array"' <<<"$rs" >/dev/null 2>&1 || refuse "could not read the rulesets of \`$DEFAULT_BRANCH\`: the number of approvals cannot be determined."
settings=$(jq -c '[.[] | select(.type == "pull_request") | .parameters // {}] as $p
  | if ($p | length) == 0 then {required: 1, mode: "strict", codeowners: false, last_push: false}
    else {required: ([$p[] | .required_approving_review_count // 0] | max),
          mode: (if any($p[]; .dismiss_stale_reviews_on_push == true) then "strict" else "basic" end),
          codeowners: any($p[]; .require_code_owner_review == true),
          last_push: any($p[]; .require_last_push_approval == true)} end' <<<"$rs")
result=$(jq -c --argjson s "$settings" '(.rules, .entries) |= map(.approvals_mode = $s.mode | .approvals_required = ($s.required | tostring))' <<<"$result") || refuse "could not apply the repository's approval settings."
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  { echo "rules=$(jq -c .rules <<<"$result")"; echo "entries=$(jq -c '.entries | map({key: .label, value: .}) | from_entries' <<<"$result")"; echo "owner_entries=$(jq -c .owner_entries <<<"$result")"; echo "codeowners=$(jq -r .codeowners <<<"$settings")"; echo "last_push=$(jq -r .last_push <<<"$settings")"; } >> "$GITHUB_OUTPUT"
fi
[ -n "${TFPLS_DRY:-}" ] && echo "$result"
exit 0
