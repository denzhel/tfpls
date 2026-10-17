#!/usr/bin/env bash
# Cut a release. On main, tfpls workflows reference their own actions as `<owner>/tfpls/...@main`,
# which would defeat pinning: a consumer on `@v0` would still run main's actions. A release therefore makes a
# commit (not on main) in which those internal references point at the exact version tag, tags it, and moves
# the major tag (v0) to it.
#   tools/release.sh [--dry-run] vX.Y.Z          a release: moves the major tag (v0) to it
#   tools/release.sh [--dry-run] vX.Y.Z-rc.N      a pre-release for testing a batch: the exact tag only, the major tag does NOT move
# Without --dry-run the script must run on a clean, pushed main; it pushes only the tags, never main.
set -euo pipefail

dry=false
if [ "${1:-}" = --dry-run ]; then dry=true; shift; fi
ver=${1:-}
[[ "$ver" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]] || { echo "usage: tools/release.sh [--dry-run] vX.Y.Z | vX.Y.Z-rc.N" >&2; exit 1; }
major=${ver%%.*}
pre=false; [[ "$ver" == *-rc.* ]] && pre=true   # a pre-release is tested in a caller by its exact tag; callers on the major tag never see it

cd "$(git rev-parse --show-toplevel)"
remote=$(git remote get-url origin 2>/dev/null || true)
slug=$(sed -E 's#^(git@github.com:|https://github.com/)##; s#\.git$##' <<<"$remote")
[ -n "$slug" ] || { echo "no origin remote to derive owner/repo from" >&2; exit 1; }

if [ "$dry" = false ]; then
  [ "$(git rev-parse --abbrev-ref HEAD)" = main ] || { echo "run on main" >&2; exit 1; }
  [ -z "$(git status --porcelain)" ] || { echo "working tree is not clean" >&2; exit 1; }
  git fetch -q origin main
  [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || { echo "HEAD is not origin/main (push or pull first)" >&2; exit 1; }
  if git rev-parse -q --verify "refs/tags/$ver" >/dev/null; then echo "tag $ver already exists; exact version tags are never moved" >&2; exit 1; fi
fi

work=$(mktemp -d)
trap 'git worktree remove --force "$work" >/dev/null 2>&1 || true; rm -rf "$work"' EXIT
git worktree add -q --detach "$work" HEAD
cd "$work"

# rewrite internal references: <slug>/actions/<name>@main and <slug>/.github/workflows/<file>@main -> @<ver>;
# README/docs/examples that tell consumers to use @main or @v0 are left as written
files=$(grep -rlE "${slug}/(actions/[a-z-]+|\.github/workflows/[a-z-]+\.yml)@main" .github/workflows actions 2>/dev/null || true)
[ -n "$files" ] || { echo "no internal @main references found" >&2; exit 1; }
# shellcheck disable=SC2086
sed -i.bak -E "s#(${slug}/(actions/[a-z-]+|\.github/workflows/[a-z-]+\.yml))@main#\1@${ver}#g" $files
find . -name '*.bak' -not -path './.git/*' -delete

left=$(grep -rnE "${slug}/(actions/[a-z-]+|\.github/workflows/[a-z-]+\.yml)@main" .github/workflows actions || true)
[ -z "$left" ] || { echo "internal @main references remain:" >&2; echo "$left" >&2; exit 1; }
for f in $files; do yq '.' "$f" >/dev/null || { echo "invalid YAML after rewrite: $f" >&2; exit 1; }; done

n=$(grep -rhoE "${slug}/(actions/[a-z-]+|\.github/workflows/[a-z-]+\.yml)@${ver}" .github/workflows actions | wc -l | tr -d ' ')
echo "release $ver: $n internal references now point at @${ver} in $(wc -w <<<"$files" | tr -d ' ') files"
[ "$pre" = true ] && echo "pre-release: $major stays where it is"
if [ "$dry" = true ]; then git diff --stat | tail -1; echo "dry run: nothing committed, tagged or pushed"; exit 0; fi

git -c user.name="tfpls release" -c user.email="noreply@users.noreply.github.com" commit -qam "release ${ver}: internal references pinned to ${ver}"
git tag -a "$ver" -m "$ver"
if [ "$pre" = false ]; then git tag -f -a "$major" -m "$major (moves to the latest ${major}.x.y release)" "${ver}^{commit}"; fi
git push origin "refs/tags/$ver"
if [ "$pre" = false ]; then
  git push --force origin "refs/tags/$major"
  echo "published $ver and moved $major"
else
  echo "published $ver (pre-release: $major not moved)"
fi
