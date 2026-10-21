#!/usr/bin/env bash
# tfpls pr-notes: the first-run notes (pipeline added by this pull request, no workspaces) against a temp repo and a gh stub.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
(cd "$here" && go build -o "$T/tfpls" ./cmd/tfpls) || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
N="$T/tfpls pr-notes"
mkdir -p "$T/bin" "$T/repo/.github/workflows"
git -C "$T/repo" init -q -b main; git -C "$T/repo" config user.email t@t; git -C "$T/repo" config user.name t
echo x > "$T/repo/README.md"; git -C "$T/repo" add -A; git -C "$T/repo" -c commit.gpgsign=false commit -qm base; BASE_NO=$(git -C "$T/repo" rev-parse HEAD)
echo "name: tofu" > "$T/repo/.github/workflows/tofu.yml"; git -C "$T/repo" add -A; git -C "$T/repo" -c commit.gpgsign=false commit -qm add; BASE_YES=$(git -C "$T/repo" rev-parse HEAD)
cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
echo "$*" >> "$STUB_LOG"
case "$*" in
  *"-X DELETE"*) echo '{}';;
  *"-X PATCH"*) for a in "$@"; do case "$a" in body=*) printf '%s\n' "${a#body=}" >> "$STUB_BODY";; esac; done; echo '{}';;
  *"-f body="*) for a in "$@"; do case "$a" in body=*) printf '%s\n' "${a#body=}" >> "$STUB_BODY";; esac; done; echo '{}';;
  *"issues/"*"/comments"*) cat "${STUB_COMMENTS:-/dev/null}" 2>/dev/null || echo '[]';;
  *) echo '{}';;
esac
STUB
chmod +x "$T/bin/gh"
run() { : > "$T/log"; : > "$T/body"; : > "$T/gho"
  ( cd "$T/repo" && env -i PATH="$T/bin:$PATH" STUB_LOG="$T/log" STUB_BODY="$T/body" STUB_COMMENTS="${STUB_COMMENTS:-}" GITHUB_OUTPUT="$T/gho" REPO=o/r PR=3 GH_TOKEN=x \
      WF_REF="o/r/.github/workflows/tofu.yml@refs/pull/3/merge" BASE_SHA="${BASE_SHA:-}" CONFIG=.tfp.yml $N "$1" >"$T/out" 2>&1; echo $?); }
out() { sed -n "s/^$1=//p" "$T/gho"; }

BASE_SHA=$BASE_NO eq "the caller workflow is not on the base commit: introduced" "$(BASE_SHA=$BASE_NO run introduced)|$(out introduced)" "0|true"
grep -q "tfpls:introduced" "$T/body" && grep -q "every workspace is planned once" "$T/body" && ok "the note says every workspace is planned once" || bad "note: $(cat "$T/body")"
eq "the caller workflow exists on the base commit: not introduced, nothing posted" "$(BASE_SHA=$BASE_YES run introduced)|$(out introduced)|$(wc -c < "$T/log" | tr -d ' ')" "0|false|0"
eq "an unknown base: not introduced" "$(BASE_SHA=deadbeef run introduced)|$(out introduced)" "0|false"
# a second run updates the bot's note; a stranger's comment with the same first line is never touched
cat > "$T/comments.json" <<'J'
[{"id":11,"user":{"type":"User"},"body":"<!-- tfpls:introduced -->\nfake"},{"id":12,"user":{"type":"Bot"},"body":"<!-- tfpls:introduced -->\nold"}]
J
STUB_COMMENTS="$T/comments.json" BASE_SHA=$BASE_NO run introduced >/dev/null
grep -q "comments/12" "$T/log" && ! grep -q "comments/11" "$T/log" && ok "the bot's own note is updated, the stranger's comment is not touched" || bad "log: $(cat "$T/log")"
run no-workspaces >/dev/null; grep -q "No workspaces were found" "$T/body" && grep -q '\.tfp\.yml' "$T/body" && grep -q 'envs/dev' "$T/body" && ok "the no-workspaces note names the config file and shows both layouts" || bad "no-workspaces note: $(cat "$T/body")"
cat > "$T/comments.json" <<'J'
[{"id":21,"user":{"type":"User"},"body":"<!-- tfpls:no-workspaces -->\nfake"},{"id":22,"user":{"type":"Bot"},"body":"<!-- tfpls:no-workspaces -->\nreal"}]
J
STUB_COMMENTS="$T/comments.json" run clear >/dev/null
grep -q "DELETE.*comments/22" "$T/log" && ! grep -q "comments/21" "$T/log" && ok "clear removes only the bot's note" || bad "clear: $(cat "$T/log")"
eq "an unknown mode is a usage error" "$(run bogus)" 2

done_tests
