#!/usr/bin/env bash
# Tests for actions/notify-slack/notify.sh with a curl stub that records the request.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
N="$here/actions/notify-slack/notify.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT; mkdir -p "$T/bin"
cat > "$T/bin/curl" <<'STUB'
#!/usr/bin/env bash
[ -n "${STUB_FAIL:-}" ] && exit 22
for a in "$@"; do last=$a; done
echo "$last" > "$STUB_DIR/url"; cat > "$STUB_DIR/body"
STUB
chmod +x "$T/bin/curl"
send() { rm -f "$T/url" "$T/body"; env -i PATH="$T/bin:$PATH" STUB_DIR="$T" REPO=o/r NAME=prod KIND=apply URL=http://run SUMMARY="+0 ~1 -0" "$@" bash "$N" 2>&1; echo "rc=$?"; }

CH='[{"action":"changed","address":"module.a.aws_iam_policy.p[0]"},{"action":"added","address":"module.b.aws_iam_role.r[\"x\"]"}]'
ctx=(AUTHOR=denzhel COMMIT_MESSAGE="Allow GHA - ABC-123" COMMIT_URL=http://commit SHA=abc123)
send HOOK=http://hook STATUS=applied "${ctx[@]}" CHANGES="$CH" >/dev/null
eq "slack default: a successful apply" "$(jq -r .text "$T/body")" 'Successful deployment on `prod` has finished
By @denzhel, <http://commit|Commit Message>: `Allow GHA - ABC-123`
<http://run|Review Logs>
```
changed: module.a.aws_iam_policy.p[0]
added: module.b.aws_iam_role.r["x"]
```'
eq "a delivered message is logged" "$(send HOOK=http://hook STATUS=applied "${ctx[@]}" | grep -c 'slack notification sent for prod (applied)')" 1
eq "the message is written to the log" "$(send HOOK=http://hook STATUS=applied "${ctx[@]}" | grep -c 'Successful deployment on')" 1
eq "slack: the webhook URL is used" "$(cat "$T/url")" http://hook
send HOOK=http://hook STATUS=error MENTION='<!subteam^S1>' "${ctx[@]}" SUMMARY="apply failed" >/dev/null
eq "slack default: a failure mentions the group and says why" "$(jq -r .text "$T/body")" '<!subteam^S1> A run on `prod` has failed
By @denzhel, <http://commit|Commit Message>: `Allow GHA - ABC-123`
<http://run|Review Logs>
apply failed'
send HOOK=http://hook STATUS=applied MENTION='<!subteam^S1>' "${ctx[@]}" >/dev/null
eq "a success does not mention anyone" "$(jq -r .text "$T/body" | grep -c 'subteam')" 0
out=$(send HOOK=http://hook STATUS=no-changes "${ctx[@]}"); eq "an apply without changes posts nothing" "$([ -f "$T/body" ] && echo sent || echo none)|$(grep -c 'rc=0' <<<"$out")" "none|1"
send HOOK=http://hook STATUS=drift MENTION='@here' KIND="drift (drifted)" "${ctx[@]}" >/dev/null
eq "slack default: drift mentions and names the workspace" "$(jq -r .text "$T/body" | head -1)" '@here Drift detected on `prod`'
send HOOK=http://hook STATUS=in-sync KIND="drift (recovered)" "${ctx[@]}" >/dev/null
eq "slack default: drift recovered" "$(jq -r .text "$T/body" | head -1)" 'Drift on `prod` is resolved'
send HOOK=http://hook STATUS=unlocked KIND="stale lock cleanup" SUMMARY="lock 12 released" "${ctx[@]}" >/dev/null
eq "slack default: a released stale lock" "$(jq -r .text "$T/body" | head -1)" 'Released a stale lock on `prod`'
send HOOK=http://hook STATUS=applied PR=7 KIND=apply AUTHOR=alice COMMIT_MESSAGE="Add queue" COMMIT_URL=http://pr/7 >/dev/null
eq "an apply by comment names the commenter and the pull request" "$(jq -r .text "$T/body" | sed -n 2p)" 'By @alice, <http://pr/7|Commit Message>: `Add queue`'
send HOOK=http://hook STATUS=applied AUTHOR=bot COMMIT_MESSAGE='a `tick` and
second line' >/dev/null
eq "backticks are neutralised and only the first line of the message is used" "$(jq -r .text "$T/body" | sed -n 2p)" 'By @bot, <|Commit Message>: `a '"'"'tick'"'"' and`'
send HOOK=http://hook STATUS=applied AUTHOR=bot >/dev/null
eq "no commit message: only the author" "$(jq -r .text "$T/body" | sed -n 2p)" 'By @bot'

# ---- custom templates
mkdir -p "$T/ws/.github/tfpls"
cat > "$T/ws/.github/tfpls/slack.jq" <<'J'
if .status == "applied" then {text: "custom \(.workspace) \(.changes | length) changes by \(.author)"} else empty end
J
tpl=(TEMPLATE=.github/tfpls/slack.jq WORKSPACE_DIR="$T/ws")
send HOOK=http://hook STATUS=applied "${ctx[@]}" "${tpl[@]}" CHANGES="$CH" >/dev/null
eq "a custom template: the body is exactly its output" "$(cat "$T/body")" '{"text":"custom prod 2 changes by denzhel"}'
out=$(send HOOK=http://hook STATUS=error "${ctx[@]}" "${tpl[@]}"); eq "a template with empty output sends nothing" "$([ -f "$T/body" ] && echo sent || echo none)|$(grep -c 'rc=0' <<<"$out")" "none|1"
echo '{text: ($ENV | keys | length | tostring), e: (env | keys | length)}' > "$T/ws/env.jq"
send HOOK=http://hook STATUS=applied "${ctx[@]}" TEMPLATE=env.jq WORKSPACE_DIR="$T/ws" SECRET_TOKEN=hunter2 >/dev/null
eq "a template sees an empty environment" "$(jq -c '[.text,.e]' "$T/body")" '["0",0]'
echo '{text: (.status |' > "$T/ws/broken.jq"
out=$(send HOOK=http://hook STATUS=applied "${ctx[@]}" TEMPLATE=broken.jq WORKSPACE_DIR="$T/ws")
eq "a broken template: warning, the default message is sent, the job stays green" "$(grep -c '::warning::notify_template failed' <<<"$out")|$(grep -c 'rc=0' <<<"$out")|$(jq -r .text "$T/body" | head -1)" '1|1|Successful deployment on `prod` has finished'
echo '"just a string"' > "$T/ws/str.jq"
out=$(send HOOK=http://hook STATUS=applied "${ctx[@]}" TEMPLATE=str.jq WORKSPACE_DIR="$T/ws")
eq "a template that does not output an object falls back" "$(grep -c 'exactly one JSON object' <<<"$out")|$(jq -r .text "$T/body" | head -1)" '1|Successful deployment on `prod` has finished'
out=$(send HOOK=http://hook STATUS=applied "${ctx[@]}" TEMPLATE=missing.jq WORKSPACE_DIR="$T/ws"); eq "a missing template falls back" "$(grep -c 'not found' <<<"$out")" 1
out=$(send HOOK=http://hook STATUS=applied "${ctx[@]}" TEMPLATE=../x.jq WORKSPACE_DIR="$T/ws"); eq "a template path outside the repository is refused" "$(grep -c 'not a relative path' <<<"$out")" 1
echo '{text: "blocks"}' > "$T/ws/t.jq"
send HOOK=http://hook STATUS=applied FORMAT=teams TEMPLATE=t.jq WORKSPACE_DIR="$T/ws" >/dev/null
eq "a template replaces the body for any format" "$(cat "$T/body")" '{"text":"blocks"}'
# from the default branch (gh stub): a pull request cannot change the message about its own apply
cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
echo "$*" > "$STUB_DIR/gh_args"; echo '{text: "from default branch"}'
STUB
chmod +x "$T/bin/gh"
send HOOK=http://hook STATUS=applied TEMPLATE=.github/tfpls/slack.jq TEMPLATE_REF=main WORKSPACE_DIR="$T/ws" >/dev/null
eq "TEMPLATE_REF: the template comes from that ref, not the checkout" "$(jq -r .text "$T/body")|$(grep -c 'contents/.github/tfpls/slack.jq?ref=main' "$T/gh_args")" "from default branch|1"
rm "$T/bin/gh"

# ---- the cap and a property check
for n in 0 1 49 50 51 120; do
  arr=$(jq -nc --argjson n "$n" '[range(0; $n) | {action: (["added","changed","deleted","replaced"][. % 4]), address: "mod.r[\"k\(.)\"].x\"q\"[\(.)]"}]')
  send HOOK=http://hook STATUS=applied "${ctx[@]}" CHANGES="$arr" >/dev/null
  text=$(jq -r .text "$T/body"); lines=$(grep -c '^\(added\|changed\|deleted\|replaced\): ' <<<"$text" || true)
  uniq_n=$(grep '^\(added\|changed\|deleted\|replaced\): ' <<<"$text" | sort -u | wc -l | tr -d ' ')
  want=$(( n > 50 ? 50 : n ))
  eq "$n changes: $want listed, each once, valid JSON, bounded" "$lines|$uniq_n|$(jq -e . "$T/body" >/dev/null && echo json)|$([ "$(wc -l < <(printf '%s\n' "$text"))" -le 56 ] && echo bounded)|$([ "$n" -gt 50 ] && grep -c "… $((n-50)) more, see Review Logs" <<<"$text" || echo 0)" "$want|$want|json|bounded|$([ "$n" -gt 50 ] && echo 1 || echo 0)"
done
# randomized: 25 arrays of 0-120 entries with quotes, brackets and spaces in the addresses
RANDOM=164
for k in $(seq 1 25); do
  n=$((RANDOM % 121))
  arr=$(jq -nc --argjson n "$n" --argjson salt "$RANDOM" '[range(0; $n) | {action: (["added","changed","deleted","replaced"][(. + $salt) % 4]), address: "m[\"a\(. * $salt)\"].r[\(.)] \"q\" ]["}]')
  send HOOK=http://hook STATUS=applied "${ctx[@]}" CHANGES="$arr" >/dev/null
  text=$(jq -r .text "$T/body"); listed=$(grep -c '^\(added\|changed\|deleted\|replaced\): ' <<<"$text" || true)
  uniq_n=$(grep '^\(added\|changed\|deleted\|replaced\): ' <<<"$text" | sort -u | wc -l | tr -d ' ')
  want=$(( n > 50 ? 50 : n )); nl=$(printf '%s\n' "$text" | wc -l | tr -d ' ')
  [ "$listed|$uniq_n" = "$want|$want" ] && jq -e . "$T/body" >/dev/null && [ "$nl" -le 56 ] || bad "random $k (n=$n): listed=$listed unique=$uniq_n lines=$nl"
done
ok "25 random change lists: each address once, at most 50, valid JSON, at most 56 lines"
send HOOK=http://hook STATUS=applied "${ctx[@]}" CHANGES='[{"action":"added","address":"a"}]' CHANGES_MORE=7 >/dev/null
eq "changes_more from the plan is reported" "$(jq -r .text "$T/body" | grep -c '… 7 more, see Review Logs')" 1
send HOOK=http://hook STATUS=applied "${ctx[@]}" CHANGES='not json' >/dev/null
eq "unparsable changes are ignored, the message still goes" "$(jq -r .text "$T/body" | head -1)" 'Successful deployment on `prod` has finished'

send HOOK=http://hook STATUS=error FORMAT=teams >/dev/null
eq "teams: a MessageCard with text and summary" "$(jq -r '[.["@type"], (.text|test("\\*\\*o/r\\*\\*")), (.summary|length > 0)]|join(",")' "$T/body")" "MessageCard,true,true"
send HOOK=http://hook STATUS=applied FORMAT=generic >/dev/null
eq "generic: plain JSON fields" "$(jq -c '[.repo,.workspace,.kind,.status,.ok,.url]' "$T/body")" '["o/r","prod","apply","applied",true,"http://run"]'
send HOOK=http://hook STATUS=applied FORMAT=generic "${ctx[@]}" CHANGES="$CH" >/dev/null
eq "generic: author, commit and changes are included" "$(jq -c '[.author,.commit.message,(.changes|length)]' "$T/body")" '["denzhel","Allow GHA - ABC-123",2]'
send HOOK=http://hook STATUS=error FORMAT=generic >/dev/null
eq "generic: ok is false for failures" "$(jq -r .ok "$T/body")" false
out=$(send HOOK='' STATUS=error); eq "no webhook: nothing is sent" "$([ -f "$T/body" ] && echo sent || echo none)" none
out=$(send HOOK=http://hook STATUS=error FORMAT=pager); eq "unknown format: warning, nothing sent, exit 0" "$(grep -c 'unknown notification format' <<<"$out")|$(grep -c 'rc=0' <<<"$out")|$([ -f "$T/body" ] && echo sent || echo none)" "1|1|none"
out=$(send HOOK=http://hook STATUS=error STUB_FAIL=1); eq "a failing webhook never fails the job" "$(grep -c 'rc=0' <<<"$out")|$(grep -c 'notification failed' <<<"$out")" "1|1"
long=$(printf 'x%.0s' $(seq 1 800)); send HOOK=http://hook STATUS=error SUMMARY="$long" >/dev/null
eq "a long summary is cut" "$([ "$(jq -r .text "$T/body" | wc -c)" -lt 700 ] && echo short)" short

done_tests
