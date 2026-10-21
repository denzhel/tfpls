#!/usr/bin/env bash
# The weekly tool-version watch (.github/workflows/tool-versions.yml, docs/dependencies.md#tool-versions).
# Compares the default OpenTofu and conftest versions (internal/setup/setup.go) and the actionlint version CI installs
# (.github/workflows/ci.yml) with the highest published GitHub release of each (drafts and pre-releases are skipped), and
# keeps exactly one open issue titled "Tool versions behind": opened when a default is behind, edited when the list changes,
# closed with a comment when every default is current. It only reads files; bumping a default stays a reviewed pull request.
#   GH_TOKEN=... REPO=owner/repo bash tools/tool-versions.sh
# Env: GH_TOKEN, REPO (default $GITHUB_REPOSITORY), RUN_URL (linked when the issue is closed), GITHUB_STEP_SUMMARY.
# Test override: TFPLS_ROOT (the tree whose files are read; default this repository). Fails (exit 1) when anything cannot
# be read, so a broken check is never reported as "current".
set -uo pipefail

root="${TFPLS_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
repo="${REPO:-${GITHUB_REPOSITORY:-}}"
title="Tool versions behind"
label=dependencies
die() { echo "::error::tool-versions: $1" >&2; exit 1; }
[ -n "$repo" ] || die "REPO (owner/repo) is not set"

# default <file> <sed -E script printing the version>: the default as written in the file
default() {
  local v; v=$(sed -nE "$2" "$root/$1" 2>/dev/null | head -1)
  [[ "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "cannot read the default version from $1"
  printf '%s' "$v"
}
# api <gh api args>: three attempts with a growing pause (a 502/504 from GitHub is usually gone seconds later)
api() {
  local n=1 out
  while :; do
    out=$(gh api "$@") && { printf '%s' "$out"; return 0; }
    [ "$n" -ge 3 ] && return 1
    sleep $(( ${TFPLS_RETRY_DELAY:-5} * n )); n=$((n + 1))
  done
}
# latest <owner/repo>: "<version> <release url>" of the highest published release among the newest 100 (not the newest by
# date: a backport of an older line can be released after a newer version). GraphQL with four fields: the REST list of
# 100 releases carries every release text and can time out (HTTP 504).
latest() {
  local json v
  json=$(api graphql -f owner="${1%%/*}" -f name="${1#*/}" -f query='query($owner: String!, $name: String!) {
    repository(owner: $owner, name: $name) { releases(first: 100, orderBy: {field: CREATED_AT, direction: DESC}) {
      nodes { tagName isDraft isPrerelease url } } } }') || die "cannot list the releases of $1"
  v=$(jq -r '[.data.repository.releases.nodes[]? | select((.isDraft | not) and (.isPrerelease | not))
              | {v: (.tagName | ltrimstr("v")), url}
              | select(.v | test("^[0-9]+\\.[0-9]+\\.[0-9]+$"))]
             | max_by(.v | split(".") | map(tonumber)) // empty | "\(.v) \(.url)"' <<<"$json" 2>/dev/null)
  [ -n "$v" ] || die "no published release of $1"
  printf '%s' "$v"
}
# newer <a> <b>: true when version b is higher than version a
newer() { jq -ne --arg a "$1" --arg b "$2" '($b | split(".") | map(tonumber)) > ($a | split(".") | map(tonumber))' >/dev/null; }

rows=""; current_list=""
# check <tool> <default> <owner/repo> <where the default is written>
check() {
  local tool=$1 cur=$2 src=$3 where=$4 got ver url
  got=$(latest "$src") || exit 1
  ver=${got%% *}; url=${got#* }
  if newer "$cur" "$ver"; then
    rows="$rows| $tool | $cur | [$ver]($url) | $where |
"
    echo "$tool: default $cur, latest $ver (behind)"
  else
    echo "$tool: default $cur, latest $ver (current)"
  fi
  current_list="$current_list${current_list:+, }$tool $cur"
}

tofu=$(default internal/setup/setup.go 's/^[[:space:]]*DefaultTofu[[:space:]]*=[[:space:]]*"([0-9.]+)".*/\1/p') || exit 1
conftest=$(default internal/setup/setup.go 's/^[[:space:]]*DefaultConftest[[:space:]]*=[[:space:]]*"([0-9.]+)".*/\1/p') || exit 1
actionlint=$(default .github/workflows/ci.yml 's/^[[:space:]]*v=([0-9.]+); sum=[0-9a-f]{64}.*/\1/p') || exit 1
check OpenTofu "$tofu" opentofu/opentofu '`internal/setup/setup.go` (`DefaultTofu`)'
check conftest "$conftest" open-policy-agent/conftest '`internal/setup/setup.go` (`DefaultConftest`)'
check actionlint "$actionlint" rhysd/actionlint '`.github/workflows/ci.yml` (`v=` and its `sum=`)'

# the open issue with exactly this title (the oldest, should a second one ever exist)
issues=$(gh issue list --repo "$repo" --state open --limit 500 --json number,title,body) || die "cannot list the open issues of $repo"
num=$(jq -r --arg t "$title" '[.[] | select(.title == $t)] | sort_by(.number) | .[0].number // empty' <<<"$issues") || die "unreadable issue list"
old=$(jq -r --arg t "$title" '[.[] | select(.title == $t)] | sort_by(.number) | .[0].body // empty' <<<"$issues" | tr -d '\r')

if [ -z "$rows" ]; then
  if [ -n "$num" ]; then
    gh issue close "$num" --repo "$repo" --comment "Every default is current: $current_list${RUN_URL:+ ([check]($RUN_URL))}." >/dev/null \
      || die "cannot close issue #$num"
    echo "all current; closed issue #$num"
  else
    echo "all current; no issue to keep"
  fi
  exit 0
fi

# the body depends only on the list, so an unchanged list leaves the issue alone
body="The default version of these tools is behind their latest release:

| Tool | Default | Latest release | Where the default is |
|---|---|---|---|
${rows}
To update one: change the default where the table says and every other place that names the old version (\`git grep -n <old version>\`: the CI check of the installed versions, input descriptions, docs). For actionlint, \`sum=\` is the SHA-256 of \`actionlint_<version>_linux_amd64.tar.gz\` from the release's checksums file. Add a line to \`CHANGELOG.md\`; CI runs the new versions.

Kept up to date by the weekly \`tool-versions\` workflow ([docs/dependencies.md](https://github.com/$repo/blob/main/docs/dependencies.md#tool-versions)): edited when the list changes, closed when every default is current."
[ -n "${GITHUB_STEP_SUMMARY:-}" ] && printf '### Tool versions behind\n\n| Tool | Default | Latest release | Where the default is |\n|---|---|---|---|\n%s\n' "$rows" >> "$GITHUB_STEP_SUMMARY"

if [ -z "$num" ]; then
  gh label create "$label" --repo "$repo" --color 0366d6 --description "Pull requests that update a dependency file" >/dev/null 2>&1 || true
  url=$(gh issue create --repo "$repo" --title "$title" --label "$label" --body "$body") || die "cannot open the issue"
  echo "opened $url"
elif [ "$old" != "$body" ]; then
  gh issue edit "$num" --repo "$repo" --body "$body" >/dev/null || die "cannot edit issue #$num"
  echo "updated issue #$num (the list changed)"
else
  echo "issue #$num unchanged (same list)"
fi
