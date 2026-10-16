#!/usr/bin/env bash
# Tests for tools/release.sh in --dry-run mode (nothing is committed, tagged or pushed). Run: bash tests/release.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT

slug=$(grep -rhoE '[A-Za-z0-9_.-]+/tfpls/(actions|\.github)' "$here/.github/workflows" | head -1 | sed -E 's#/tfpls/(actions|\.github)$#/tfpls#')
[ -n "$slug" ] && ok "found the repository slug used in internal references ($slug)" || { bad "slug"; exit 1; }

# work on a throwaway clone with a GitHub-style origin URL (needed to derive owner/repo); the dry run never touches the network
git clone -q "$here" "$T/clone" && git -C "$T/clone" remote set-url origin "https://github.com/$slug.git"
# include uncommitted changes of the working tree in the clone, so the test checks what is about to be committed
( cd "$here" && git ls-files -m -o --exclude-standard | while read -r f; do mkdir -p "$T/clone/$(dirname "$f")"; cp "$f" "$T/clone/$f" 2>/dev/null || true; done )
git -C "$T/clone" -c user.email=t@t -c user.name=t -c commit.gpgsign=false add -A >/dev/null
git -C "$T/clone" -c user.email=t@t -c user.name=t -c commit.gpgsign=false commit -qm wip --allow-empty

tags_before=$(git -C "$T/clone" tag | sort)
out=$(cd "$T/clone" && bash tools/release.sh --dry-run v9.9.9 2>&1); rc=$?
[ $rc -eq 0 ] && ok "dry run succeeds" || bad "dry run failed: $out"
case "$out" in *"internal references now point at @v9.9.9"*) ok "reports the rewritten references";; *) bad "report: $out";; esac
case "$out" in *"nothing committed, tagged or pushed"*) ok "dry run changes nothing";; *) bad "dry run message";; esac
[ "$(git -C "$T/clone" tag | sort)" = "$tags_before" ] && ok "no tag created" || bad "tag created"
[ -z "$(git -C "$T/clone" status --porcelain)" ] && ok "clone left clean" || bad "clone dirty"
[ -z "$(git -C "$T/clone" worktree list | grep -v "$T/clone ")" ] && ok "temporary worktree removed" || bad "worktree left behind"

for v in v1 1.0.0 v1.0 v1.0.0-rc1 ""; do
  (cd "$T/clone" && bash tools/release.sh --dry-run "$v" >/dev/null 2>&1) && bad "accepted bad version '$v'" || ok "rejects version '$v'"
done

done_tests
