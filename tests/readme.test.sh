#!/usr/bin/env bash
# The README stays short and keeps its opening: the hero block and the first paragraph are byte for byte those of main,
# the file is under 60 lines, and the quick start is the onboarding one-liner.
# Run: bash tests/readme.test.sh [file]   (a file instead of README.md, for example a copy to try a change on)
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
readme=${1:-$here/README.md}
[ -f "$readme" ] || { echo "no such file: $readme"; exit 1; }
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT

# the opening on main (hero block, blank line, first paragraph); it changes only on the owner's word, then here too
cat > "$tmp/opening" <<'EOF'
<p align="center">
  <img src="docs/assets/tfpls-hero.svg" alt="tfpls: a git diff that removes hope and adds a plan someone read. Commit message: grow up. TF, please." width="720">
</p>

**Plan on every pull request. Apply with one comment.** Revolutionary, we know: you look at the plan *before* you apply it. tfpls is OpenTofu on GitHub Actions for AWS and S3 state: reusable workflows that run in your repository, on your Actions minutes. Nothing to host, no server of ours in the path.
EOF
n=$(wc -l < "$tmp/opening" | tr -d ' ')
head -n "$n" "$readme" > "$tmp/readme"
if cmp -s "$tmp/opening" "$tmp/readme"; then ok "the hero block and the first paragraph are unchanged, byte for byte"
else bad "the hero block or the first paragraph changed:"; diff "$tmp/opening" "$tmp/readme"; fi

# the same against main itself where the history is at hand (a clone; CI checks out a single commit of a pull request)
if git -C "$here" rev-parse --verify -q origin/main >/dev/null 2>&1; then
  git -C "$here" show origin/main:README.md 2>/dev/null | head -n "$n" > "$tmp/main"
  cmp -s "$tmp/main" "$tmp/readme" && ok "the opening is byte-identical to the README of origin/main" \
    || bad "the opening differs from the README of origin/main"
else echo "skip origin/main is not in this checkout: compared with the copy above only"; fi

lines=$(wc -l < "$readme" | tr -d ' ')
[ "$lines" -lt 60 ] && ok "the README has $lines lines (under 60)" || bad "the README has $lines lines, the limit is 59"

# one way in: the onboarding one-liner (what onboarding does in detail is in docs/getting-started.md)
grep -qF 'curl -fsSL https://raw.githubusercontent.com/denzhel/tfpls/v0/tools/onboard | bash' "$readme" && [ -x "$here/tools/onboard" ] \
  && ok "the quick start is the onboarding one-liner, and tools/onboard exists" \
  || bad "the quick start lacks the onboarding one-liner, or tools/onboard is missing"
done_tests
