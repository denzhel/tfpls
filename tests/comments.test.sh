#!/usr/bin/env bash
# PR comment formatting: safe plan clipping (actions/tofu/format.sh) and marking outdated comments (actions/mark-outdated).
# Run: bash tests/comments.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/actions/tofu/format.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT

# ---- tfpls_clip_plan
printf 'small plan\nPlan: 1 to add, 0 to change, 0 to destroy.\n' > "$T/small.txt"
[ "$(tfpls_clip_plan "$T/small.txt" 45000)" = "$(cat "$T/small.txt")" ] && ok "a small plan is untouched" || bad "small plan changed"
[ -z "$(tfpls_clip_plan "$T/missing.txt" 100)" ] && ok "a missing file gives empty output" || bad "missing file"

{ for i in $(seq 1 6000); do echo "  # resource line number $i with some attribute = \"value-$i\""; done; echo "Plan: 6000 to add, 0 to change, 0 to destroy."; } > "$T/big.txt"
size=$(wc -c < "$T/big.txt"); out=$(tfpls_clip_plan "$T/big.txt" 20000)
[ "$size" -gt 100000 ] && ok "fixture is large ($size bytes)" || bad "fixture too small"
[ "$(printf '%s' "$out" | wc -c)" -lt 21000 ] && ok "a large plan is clipped to about the limit" || bad "not clipped: $(printf '%s' "$out" | wc -c)"
case "$out" in *"bytes omitted"*"run logs"*) ok "clipped plan says it was clipped and where the full text is";; *) bad "no note";; esac
printf '%s' "$out" | tail -1 | grep -q "Plan: 6000 to add" && ok "the final summary line is kept" || bad "tail lost"
head -1 <<<"$out" | grep -q "resource line number 1 " && ok "the beginning is kept" || bad "head lost"

# multibyte characters cut at the boundary must not leave invalid UTF-8
python3 - "$T/utf.txt" <<'PY'
import sys
open(sys.argv[1],"w",encoding="utf-8").write(("ש" * 30000) + "\nPlan: 1 to add\n" + ("é" * 30000))
PY
out=$(tfpls_clip_plan "$T/utf.txt" 10001)
printf '%s' "$out" | iconv -f UTF-8 -t UTF-8 >/dev/null 2>&1 && ok "output is valid UTF-8 even when the cut falls inside a character" || bad "invalid UTF-8"
# plan text produced by the tool can contain invalid bytes too
printf 'ok \xff\xfe broken\nPlan: 1 to add\n' > "$T/bad.txt"
tfpls_clip_plan "$T/bad.txt" 45000 | iconv -f UTF-8 -t UTF-8 >/dev/null 2>&1 && ok "invalid bytes in the input are dropped" || bad "invalid bytes kept"

# ---- the plan comment layout
cat > "$T/show.txt" <<'P'
Terraform will perform the following actions:

  # aws_sqs_queue.a will be updated in-place
  ~ resource "aws_sqs_queue" "a" {
      ~ message_retention_seconds = 1209600 -> 1123200
    }

Plan: 1 to add, 1 to change, 0 to destroy.
P
M="<!-- tfpls:terraform/dev -->"
b=$(tfpls_comment_body "$M" "📝" planned dev "https://run/1" "$T/show.txt" "+1 ~1 -0" "" 45000)
[ "$(sed -n 1p <<<"$b")" = "$M" ] && ok "the marker stays on the first line" || bad "marker line"
[ "$(sed -n 2p <<<"$b")" = "<!-- tfpls-status: planned -->" ] && ok "the status is a hidden second line" || bad "status line"
[ "$(sed -n 3p <<<"$b")" = "**Plan:** 1 to add, 1 to change, 0 to destroy." ] && ok "summary line from tofu's own Plan: line" || bad "summary line: $(sed -n 3p <<<"$b")"
[ "$(sed -n 4p <<<"$b")" = "### 📝 [dev](https://run/1) plan logs" ] && ok "heading links to the run" || bad "heading: $(sed -n 4p <<<"$b")"
grep -q '<details><summary>Click here to expand</summary>' <<<"$b" && grep -q 'message_retention_seconds = 1209600 -> 1123200' <<<"$b" && ok "the plan is inside one collapsed section" || bad "details"
[ "$(grep -c '<details>' <<<"$b")" = 1 ] && ok "exactly one collapsed section" || bad "details count"
tail -1 <<<"$b" | grep -q '`/tofu apply dev` to apply' && ok "apply hint at the end" || bad "hint"
grep -q 'Run logs\|Full plan output' <<<"$b" && bad "old layout remains" || ok "no separate run-logs link and no old section title"
b2=$(TFPLS_NO_HINT=1 tfpls_comment_body "$M" "📝" planned dev "https://run/1" "$T/show.txt" "+1 ~1 -0" "" 45000)
grep -q '/tofu apply' <<<"$b2" && bad "hint shown although no plan was saved" || ok "no apply hint when no plan was saved"
b2=$(tfpls_comment_body "$M" "❌" error dev "https://run/1" "$T/show.txt" "" "" 45000)
grep -q '/tofu apply' <<<"$b2" && bad "hint on an error comment" || ok "an error comment has no apply hint"
b=$(tfpls_comment_body "$M" "✅" applied dev "https://run/1" "$T/show.txt" "" "" 45000)
sed -n 4p <<<"$b" | grep -q 'apply logs' && ok "heading says apply logs after an apply" || bad "apply heading"
b=$(tfpls_comment_body "$M" "🛑" policy-failed dev "https://run/1" "$T/show.txt" "" "⚠️ notice A" 45000)
{ [ "$(grep -n 'notice A' <<<"$b" | cut -d: -f1)" -lt "$(grep -n '<details>' <<<"$b" | cut -d: -f1)" ]; } && ok "notices stay outside the collapsed section" || bad "notice position"
printf 'aws_sqs_queue.a: Refreshing state... [id=x]\ndata.aws_x.y: Reading...\ndata.aws_x.y: Read complete after 0s [id=z]\n  # aws_sqs_queue.a will be updated in-place\nPlan: 0 to add, 1 to change, 0 to destroy.\n' > "$T/raw.txt"
out=$(tfpls_strip_refresh < "$T/raw.txt")
[ "$(grep -c 'Refreshing state\|Reading\.\.\.\|Read complete' <<<"$out")" = 0 ] && grep -q 'will be updated in-place' <<<"$out" && ok "refresh, reading and read-complete lines are filtered" || bad "filter"
printf 'Error: boom\n' > "$T/err.txt"
b=$(tfpls_comment_body "$M" "❌" error dev "https://run/1" "$T/err.txt" "plan failed" "hint: unlock" 45000)
[ "$(sed -n 3p <<<"$b")" = "**plan failed**" ] && grep -q 'Error: boom' <<<"$b" && grep -q 'hint: unlock' <<<"$b" && ok "a failed plan shows the error text and the hint" || bad "error body"
b=$(tfpls_comment_body "$M" "🟢" no-changes dev "https://run/1" "$T/err.txt" "No changes." "" 45000)
[ "$(sed -n 3p <<<"$b")" = "**No changes.**" ] && ! grep -q '<details>' <<<"$b" && ! grep -q '/tofu apply' <<<"$b" && ok "no changes: no collapsed section and no apply hint" || bad "no-changes body"
{ for i in $(seq 1 6000); do echo "  # resource $i"; done; echo "Plan: 6000 to add, 0 to change, 0 to destroy."; } > "$T/huge.txt"
b=$(tfpls_comment_body "$M" "📝" planned dev "https://run/1" "$T/huge.txt" "" "" 20000)
[ "$(printf '%s' "$b" | wc -c)" -lt 22000 ] && sed -n 3p <<<"$b" | grep -q '6000 to add' && ok "a huge plan is clipped but the summary line stays" || bad "clip in body"

# ---- mark-outdated with a stub gh
mkdir "$T/bin"; cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
case "$*" in
  *"-X PATCH"*) shift; while [ $# -gt 0 ]; do case "$1" in repos/*) url=$1;; esac; [ "$1" = -f ] && body=${2#body=}; shift; done
                printf '%s\n' "$url" >> "$STUB_DIR/patched"; printf '%s\n----\n' "$body" >> "$STUB_DIR/bodies"; echo '{}';;
  *comments*)   cat "$STUB_COMMENTS";;
esac
STUB
chmod +x "$T/bin/gh"
cat > "$T/comments.json" <<'J'
[
 {"id":1,"user":{"type":"Bot"},"body":"<!-- tfpls:terraform/dev -->\n### 📝 `dev` — planned\nplan A"},
 {"id":2,"user":{"type":"Bot"},"body":"<!-- tfpls:terraform/prod -->\n### 📝 `prod` — planned\nplan B"},
 {"id":3,"user":{"type":"Bot"},"body":"<!-- tfpls:terraform/stg -->\n### 📝 `stg` — planned\n> ⏭️ **Outdated:** this workspace is not affected by the latest commit (`1234567`). The plan below is from an earlier commit and is kept for reference only.\nplan C"},
{"id":6,"user":{"type":"Bot"},"body":"<!-- tfpls:introduced -->\nℹ️ note"},
 {"id":7,"user":{"type":"Bot"},"body":"<!-- tfpls:no-workspaces -->\nℹ️ note"},
 {"id":5,"user":{"type":"User"},"body":"<!-- tfpls:terraform/stg -->\nstranger marker"},
 {"id":4,"body":"just a human comment, mentions <!-- tfpls: in the middle"}
]
J
mark() { rm -f "$T/patched" "$T/bodies"; env -i PATH="$T/bin:$PATH" STUB_DIR="$T" STUB_COMMENTS="$T/comments.json" GH_TOKEN=x GITHUB_REPOSITORY=o/r PR=7 SHA=abcdef1234567 NAMES="$(jq -c '[.[].name]' <<<"$1")" bash "$here/actions/mark-outdated/mark.sh" >/dev/null 2>&1; rc=$?; }
mark '{"terraform/dev":{"name":"terraform/dev"}}'
[ $rc -eq 0 ] && ok "mark.sh succeeds" || bad "rc=$rc"
grep -q "comments/5" "$T/patched" 2>/dev/null && bad "a stranger's comment with the marker was edited" || ok "a stranger's comment that starts with the marker is never edited"
[ "$(cat "$T/patched" 2>/dev/null)" = "repos/o/r/issues/comments/2" ] && ok "only the comment of the unselected workspace is updated" || bad "patched: $(cat "$T/patched" 2>/dev/null | tr '\n' ' ')"
grep -q "Outdated" "$T/bodies" && grep -q "abcdef1" "$T/bodies" && ok "the banner names the latest commit" || bad "banner"
head -1 "$T/bodies" | grep -q '^<!-- tfpls:terraform/prod -->$' && ok "the marker stays on the first line (the sticky comment is still found)" || bad "marker moved"
grep -q "plan B" "$T/bodies" && ok "the old plan is kept below the banner" || bad "old plan lost"
mark '{"terraform/dev":{"name":"terraform/dev"},"terraform/prod":{"name":"terraform/prod"}}'
[ ! -s "$T/patched" ] && ok "nothing is marked when every commented workspace is still selected" || bad "unexpected patch"
mark '{}'
[ "$(sort "$T/patched" | tr '\n' ' ')" = "repos/o/r/issues/comments/1 repos/o/r/issues/comments/2 " ] && ok "nothing selected => every unmarked plan comment is marked; already-marked, human comments and the pipeline notes (introduced, no-workspaces) are not" || bad "patched: $(sort "$T/patched" | tr '\n' ' ')"

done_tests
