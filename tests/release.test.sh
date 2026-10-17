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

for v in v1 1.0.0 v1.0 v1.0.0-rc1 v1.0.0-rc. v1.0.0-rc.x v1.0.0-rc.1x v1.0.0-beta.1 v1.0.0-rc.1-x ""; do
  (cd "$T/clone" && bash tools/release.sh --dry-run "$v" >/dev/null 2>&1) && bad "accepted bad version '$v'" || ok "rejects version '$v'"
done

# a pre-release (vX.Y.Z-rc.N) pins the references to its exact tag and says that the major tag stays
out=$(cd "$T/clone" && bash tools/release.sh --dry-run v9.9.9-rc.1 2>&1); rc=$?
[ $rc -eq 0 ] && ok "dry run of a pre-release succeeds" || bad "pre-release dry run failed: $out"
case "$out" in *"internal references now point at @v9.9.9-rc.1"*) ok "a pre-release pins the references to the exact rc tag";; *) bad "pre-release report: $out";; esac
case "$out" in *"pre-release: v9 stays where it is"*) ok "a pre-release dry run says the major tag stays";; *) bad "no 'stays' note: $out";; esac
out=$(cd "$T/clone" && bash tools/release.sh --dry-run v9.9.9 2>&1)
case "$out" in *"stays where it is"*) bad "a plain release claims the major tag stays";; *) ok "a plain release does not claim that (negative case)";; esac

# real runs against a local bare "origin" (relative URL = the slug, so the script derives it like for GitHub)
git clone -q "$here" "$T/real" && git -C "$T/real" checkout -q -B main
( cd "$here" && git ls-files -m -o --exclude-standard | while read -r f; do mkdir -p "$T/real/$(dirname "$f")"; cp "$f" "$T/real/$f" 2>/dev/null || true; done )
mkdir -p "$T/real/$slug" && git init -q --bare "$T/real/$slug" && echo "${slug%%/*}/" >> "$T/real/.git/info/exclude"
G() { git -C "$T/real" -c user.email=t@t -c user.name=t -c commit.gpgsign=false "$@"; }
G add -A >/dev/null; G commit -qm wip --allow-empty
G remote set-url origin "$slug"
G config remote.origin.pushurl "$T/real/$slug"   # the script pushes from a temporary worktree: an absolute push URL; the relative fetch URL is what it derives the slug from
G push -q origin main && G tag v9 && G push -q origin refs/tags/v9
v9_before=$(git --git-dir="$T/real/$slug" rev-parse v9)
out=$(cd "$T/real" && bash tools/release.sh v9.9.9-rc.1 2>&1); rc=$?
[ $rc -eq 0 ] && ok "a real pre-release run succeeds" || bad "real pre-release failed ($rc): $out"
case "$out" in *"published v9.9.9-rc.1 (pre-release: v9 not moved)"*) ok "the run reports that the major tag was not moved";; *) bad "real pre-release output: $out";; esac
rt() { git --git-dir="$T/real/$slug" tag | sort | tr '\n' ' '; }
eq "only the rc tag was added to the remote" "$(rt)" "v9 v9.9.9-rc.1 "
eq "the major tag still points at its previous commit (negative case)" "$(git --git-dir="$T/real/$slug" rev-parse v9)" "$v9_before"
git --git-dir="$T/real/$slug" show "v9.9.9-rc.1:.github/workflows/tofu.yml" | grep -q "plan.yml@v9.9.9-rc.1" && ok "the rc commit pins the internal references to the rc tag" || bad "references not pinned in the rc commit"
[ "$(git --git-dir="$T/real/$slug" rev-parse "v9.9.9-rc.1^{commit}")" != "$(git --git-dir="$T/real/$slug" rev-parse main)" ] && ok "the rc commit is not on main" || bad "rc commit is main"
(cd "$T/real" && bash tools/release.sh v9.9.9-rc.1 >/dev/null 2>&1) && bad "an existing rc tag was accepted again" || ok "an existing rc tag is never moved or re-created"
out=$(cd "$T/real" && bash tools/release.sh v9.9.9 2>&1); rc=$?
[ $rc -eq 0 ] && ok "a real release of a plain version succeeds" || bad "real release failed ($rc): $out"
case "$out" in *"published v9.9.9 and moved v9"*) ok "a plain release still moves the major tag";; *) bad "plain release output: $out";; esac
[ "$(git --git-dir="$T/real/$slug" rev-parse "v9^{commit}")" = "$(git --git-dir="$T/real/$slug" rev-parse "v9.9.9^{commit}")" ] && ok "the major tag now points at the released commit" || bad "major tag not moved"

done_tests
