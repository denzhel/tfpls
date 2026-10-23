#!/usr/bin/env bash
# The apply after a merge (apply.yml) when `/tofu apply` already applied the workspace: the real `tfpls tofu` with a stub
# gh, tofu and git. The consumed reviewed plan is not tried again; one plan confirms that nothing is left, and one compact
# comment is posted. A merge without a prior apply, a commit planned after the apply, no pull request number, a stranger's
# comment and a failing API behave as before. Run: bash tests/run-after-apply.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
. "$here/tests/plan-store-shim.sh"   # tfpls_pack for the fixture plan (the store itself is Go)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin"
# the binary under test; the shell of the test packs its fixture plan with the same one
build_bin tfpls "$T/bin/tfpls" || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
export TFPLS_PLAN_STORE_BIN="$T/bin/tfpls"
SHA=abcdef0123456789abcdef0123456789abcdef01
RUN=https://github.com/o/r/actions/runs   # this run is $RUN/222; the /tofu apply run in the comments is $RUN/111

# ---- stubs
cat > "$T/bin/tofu" <<'STUB'
#!/usr/bin/env bash
# the result of a new plan comes from STUB_PLAN (none | update | fail); with STUB_STALE the saved reviewed plan is stale (it
# was applied already: OpenTofu applies a saved plan only once). Every call is logged; every applied plan too.
echo "$*" >> "$STUB_DIR/tofu.log"
last=""; out=""; for a in "$@"; do last=$a; case "$a" in -out=*) out=${a#-out=};; esac; done
case "$1" in
  version) echo '{"terraform_version":"1.13.1"}';;
  plan)
    case "${STUB_PLAN:-none}" in
      none)   echo "No changes. Your infrastructure matches the configuration."; exit 0;;
      update) echo fresh > "$out"; printf '  # aws_sqs_queue.a will be updated in-place\n\nPlan: 0 to add, 1 to change, 0 to destroy.\n'; exit 2;;
      fail)   echo "Error: boom"; exit 1;;
    esac;;
  show)
    kind=$(cat "$last")
    if [ "$2" = -json ]; then
      case "$kind" in
        reviewed) echo '{"resource_changes":[{"address":"aws_sqs_queue.a","change":{"actions":["create"]}}]}';;
        *)        echo '{"resource_changes":[{"address":"aws_sqs_queue.a","change":{"actions":["update"]}}]}';;
      esac
    else
      case "$kind" in
        reviewed) printf '  # aws_sqs_queue.a will be created\n\nPlan: 1 to add, 0 to change, 0 to destroy.\n';;
        *)        printf '  # aws_sqs_queue.a will be updated in-place\n\nPlan: 0 to add, 1 to change, 0 to destroy.\n';;
      esac
    fi;;
  apply)
    kind=$(cat "$last"); echo "$kind" >> "$STUB_DIR/applied"
    if [ "$kind" = reviewed ] && [ -n "${STUB_STALE:-}" ]; then printf 'Error: Saved plan is stale\n'; exit 1; fi
    if [ "$kind" = reviewed ]; then printf 'aws_sqs_queue.a: Creation complete after 1s\n\nApply complete! Resources: 1 added, 0 changed, 0 destroyed.\n'
    else printf 'aws_sqs_queue.a: Modifications complete after 1s\n\nApply complete! Resources: 0 added, 1 changed, 0 destroyed.\n'; fi;;
esac
exit 0
STUB
cat > "$T/bin/git" <<'STUB'
#!/usr/bin/env bash
# the tree of the merged commit, the one the reviewed plan was made for
case "$*" in *rev-parse*) echo tree0123456789;; esac
STUB
cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
# The pull request's comments come from STUB_COMMENTS (it can hold several pages, as `gh api --paginate` prints them); with
# STUB_FAIL_LIST listing them fails. A posted comment is kept in posted. The reviewed plan is one artifact of the pull
# request's head commit; downloading it copies STUB_PLAN_FILE.
if [ "$1" = run ] && [ "$2" = download ]; then
  echo "run download" >> "$STUB_DIR/gh.log"
  while [ $# -gt 0 ]; do [ "$1" = -D ] && d=$2; shift; done
  mkdir -p "$d" && cp "$STUB_PLAN_FILE" "$d/"; exit
fi
method=GET; path=""; body=""; shift
while [ $# -gt 0 ]; do
  case "$1" in
    -X) method=$2; shift;;
    -f) case "$2" in body=*) body=${2#body=}; [ "$method" = GET ] && method=POST;; esac; shift;;
    repos/*) path=$1;;
  esac
  shift
done
echo "$method $path" >> "$STUB_DIR/gh.log"
case "$method $path" in
  "GET repos/o/r/issues/7/comments?per_page=100") [ -z "${STUB_FAIL_LIST:-}" ] || exit 1; cat "$STUB_COMMENTS";;
  "POST repos/o/r/issues/7/comments") printf '%s\n' "$body" > "$STUB_DIR/posted"; echo '{"id":999,"html_url":"https://github.com/o/r/pull/7#issuecomment-999"}';;
  "PATCH repos/o/r/issues/comments/"*) echo '{}';;
  "GET repos/o/r/actions/artifacts?"*)
    printf '{"artifacts":[{"id":5,"expired":false,"created_at":"2026-10-01T00:00:00Z","workflow_run":{"id":55,"repository_id":100,"head_repository_id":100,"head_branch":"feature","head_sha":"%s"}}]}\n' "$HEAD_SHA";;
  *) exit 1;;
esac
STUB
chmod +x "$T/bin/tofu" "$T/bin/git" "$T/bin/gh"

# the reviewed plan saved for the pull request's head commit (unencrypted: the run has no key)
mkdir -p "$T/saved"; echo reviewed > "$T/saved/tfplan"
jq -n --arg sha "$SHA" '{name:"terraform/dev",dir:"stack",tofu_version:"1.13.1",tree:"tree0123456789",head_sha:$sha,targets:"",created_at:"2026-10-01T00:00:00Z"}' > "$T/meta.json"
( unset PLAN_ENCRYPTION_KEY; tfpls_pack "$T/saved" "$T/art" "$T/meta.json" >/dev/null ) && [ -s "$T/art/plan.tgz" ] || bad "could not pack the fixture plan"

# ---- the pull request's comments
# c <id> <user type> <status> <run link> [superseded]: a comment of terraform/dev laid out as run.sh posts it
c() {
  jq -cn --argjson id "$1" --arg type "$2" --arg s "$3" --arg url "$4" --arg sup "${5:-}" '
    (if $s == "applied" then "✅ [dev](\($url)) apply logs" else "📝 [dev](\($url)) plan logs" end) as $h
    | {id: $id, user: {type: $type}, html_url: "https://github.com/o/r/pull/7#issuecomment-\($id)",
       body: (["<!-- tfpls:terraform/dev -->"]
              + (if $sup != "" then ["> 🔁 **Superseded** by https://github.com/o/r/pull/7#issuecomment-1. The plan below is kept for reference only."] else [] end)
              + ["<!-- tfpls-status: \($s) -->", "**x**", "### \($h)", ""] | join("\n"))}'
}
page() { local IFS=,; printf '[%s]\n' "$*"; }
P1=$(c 101 Bot planned "$RUN/100" superseded)    # the pull request plan
A1=$(c 102 Bot applied "$RUN/111")               # /tofu apply
HUMAN='{"id":103,"user":{"type":"User"},"html_url":"https://github.com/o/r/pull/7#issuecomment-103","body":"applied, merging"}'
OTHER=$(jq -cn '{id: 104, user: {type: "Bot"}, html_url: "x", body: "<!-- tfpls:terraform/prod -->\n<!-- tfpls-status: planned -->\n**x**"}')
page "$P1" "$HUMAN" "$A1" "$OTHER" > "$T/consumed.json"
page "$P1" "$HUMAN" > "$T/planned.json"

# go [VAR=value ...]: the run after pull request 7 was merged, as apply.yml starts it (apply auto, plan mode reviewed, the head
# SHA and the number of the merged pull request), for terraform/dev with auto_apply always; arguments override the environment
go() {
  rm -rf "$T/repo" "$T/tmp" "$T/posted" "$T/applied"
  mkdir -p "$T/repo/stack" "$T/tmp"; : > "$T/gho"; : > "$T/summary"; : > "$T/tofu.log"; : > "$T/gh.log"
  env -i PATH="$T/bin:$PATH" HOME="$T" STUB_DIR="$T" STUB_PLAN_FILE="$T/art/plan.tgz" STUB_COMMENTS="$T/consumed.json" \
    GITHUB_REPOSITORY=o/r GITHUB_REPOSITORY_ID=100 DEFAULT_BRANCH=main GITHUB_OUTPUT="$T/gho" GITHUB_STEP_SUMMARY="$T/summary" \
    RUNNER_TEMP="$T/tmp" REPO_ROOT="$T/repo" RUN_URL="$RUN/222" GH_TOKEN=x \
    ENTRY='{"name":"terraform/dev","label":"dev","dir":"stack","workspace":"","auto_apply":"always"}' \
    TARGETS="" APPLY=auto PR=7 PLAN_MODE=reviewed HEAD_SHA="$SHA" "$@" \
    "$T/bin/tfpls" tofu > "$T/out" 2>&1
  rc=$?
}
st()      { grep '^status=' "$T/gho" | tail -1 | cut -d= -f2-; }
fetched() { grep -c 'actions/artifacts\|run download' "$T/gh.log"; }
listed()  { grep -c '^GET repos/o/r/issues/7/comments' "$T/gh.log"; }
posts()   { grep -c '^POST repos/o/r/issues/7/comments$' "$T/gh.log"; }
plans()   { grep -c '^plan ' "$T/tofu.log"; }
applied() { cat "$T/applied" 2>/dev/null | tr '\n' ' '; }
stale()   { grep -c 'reviewed plan is stale' "$T/out"; }
# the posted comment is the compact one (other comments of a consumed plan carry a note that starts with the same words)
compact() { grep -cE '^🟢 \*\*\[dev\]\([^)]*\)\*\*: Already applied by `/tofu apply` \(\[run\]\([^)]*\)\); nothing changed since\.$' "$T/posted" 2>/dev/null; }
LINE3="🟢 **[dev]($RUN/222)**: Already applied by \`/tofu apply\` ([run]($RUN/111)); nothing changed since."

# ---- 1. /tofu apply applied the workspace, then the pull request was merged: the reviewed plan is consumed
go STUB_STALE=1
eq "consumed: the run succeeds with the status no-changes (what readers of the status saw before)" "$rc|$(st)" "0|no-changes"
eq "consumed: the reviewed plan is neither fetched nor applied, and no 'reviewed plan is stale' line" "$(fetched)|$(applied)|$(stale)" "0||0"
eq "consumed: exactly one plan, to confirm that nothing is left" "$(plans)" "1"
eq "consumed: exactly one comment is posted" "$(posts)" "1"
eq "consumed: the comment is the compact one (marker, status no-changes, one line)" "$(sed -n 1p "$T/posted")|$(sed -n 2p "$T/posted")|$(sed -n 3p "$T/posted")" "<!-- tfpls:terraform/dev -->|<!-- tfpls-status: no-changes -->|$LINE3"
eq "consumed: no plan heading, no collapsed plan, no apply hint" "$(grep -c 'plan logs\|<details>\|/tofu apply dev' "$T/posted")|$(grep -c . "$T/posted")" "0|3"
grep -q "::notice::.*already applied by /tofu apply ($RUN/111)" "$T/out" && ok "consumed: the run log says why the reviewed plan is not applied" || bad "consumed: no notice in the log: $(cat "$T/out")"
grep -q "already applied by \`/tofu apply\` ($RUN/111)" "$T/summary" && ok "consumed: the job summary says it too" || bad "consumed: job summary: $(cat "$T/summary")"
eq "consumed: the outputs say that no reviewed plan was used, and why there are no changes" "$(grep '^reviewed=' "$T/gho")|$(grep '^summary=' "$T/gho")" 'reviewed=false|summary=Already applied by `/tofu apply`; nothing changed since.'

# the comments are paginated: the apply comment can be on a later page
{ page "$P1" "$(for i in $(seq 1 99); do jq -cn --argjson id "$((200 + i))" '{id: $id, user: {type: "User"}, body: "comment"}'; done | paste -sd, -)"; page "$(c 400 Bot applied "$RUN/111")"; } > "$T/paged.json"
go STUB_STALE=1 STUB_COMMENTS="$T/paged.json"
eq "consumed, the apply comment on the second page: compact comment, nothing fetched or applied" "$(st)|$(compact)|$(fetched)|$(applied)" "no-changes|1|0|"

# a plan without changes after the apply (a commit that changes nothing) is not a newer reviewed plan
page "$P1" "$A1" "$(c 105 Bot no-changes "$RUN/120")" > "$T/nochange.json"
go STUB_STALE=1 STUB_COMMENTS="$T/nochange.json"
eq "consumed, a no-changes plan after the apply: compact comment, nothing fetched" "$(st)|$(compact)|$(fetched)" "no-changes|1|0"

# ---- 2. the fresh plan finds changes (something changed after the apply): the usual auto_apply rules
go STUB_STALE=1 STUB_PLAN=update
eq "consumed + changes, auto_apply always: the new plan is applied, never the consumed one" "$rc|$(st)|$(applied)|$(stale)|$(fetched)" "0|applied|fresh |0|0"
grep -qF "Already applied by \`/tofu apply\` ([run]($RUN/111))" "$T/posted" && grep -q '^\*\*Applied:\*\* 0 added, 1 changed, 0 destroyed\.$' "$T/posted" \
  && [ "$(compact)" = 0 ] && ok "consumed + changes: the usual apply comment, with a note that /tofu apply had applied the reviewed plan" || bad "consumed + changes comment: $(cat "$T/posted")"
go STUB_STALE=1 STUB_PLAN=update AUTO_APPLY=additive_only
eq "consumed + an update, auto_apply additive_only: skipped, nothing applied" "$(st)|$(applied)|$(grep -c 'Auto-apply not allowed' "$T/posted")" "skipped||1"
go STUB_STALE=1 STUB_PLAN=fail
eq "consumed + a failing plan: an error, nothing applied" "$rc|$(st)|$(applied)|$(compact)" "1|error||0"

# ---- 3. require_reviewed_plan (plan mode reviewed_only) never applies a new plan
go STUB_STALE=1 PLAN_MODE=reviewed_only
eq "reviewed_only + consumed: compact comment, the run is green (it failed before)" "$rc|$(st)|$(compact)|$(applied)|$(fetched)" "0|no-changes|1||0"
go STUB_STALE=1 PLAN_MODE=reviewed_only STUB_PLAN=update
eq "reviewed_only + consumed + changes: refused, nothing applied" "$rc|$(st)|$(applied)" "1|error|"
grep -q '^summary=No reviewed plan to apply: `/tofu apply` already applied the reviewed plan' "$T/gho" && grep -q '::error::' "$T/out" \
  && ok "reviewed_only + consumed + changes: the summary and the log say why" || bad "refusal text: $(grep '^summary=' "$T/gho")"

# ---- 4. auto_apply never: no reviewed plan is fetched anyway, but the comment is the compact one too
go STUB_STALE=1 AUTO_APPLY=never
eq "never + consumed: compact comment, nothing fetched or applied" "$(st)|$(compact)|$(fetched)|$(applied)" "no-changes|1|0|"

# ---- negative cases: the behaviour of before
go STUB_COMMENTS="$T/planned.json"
eq "a merge without a prior apply: the reviewed plan is applied (as before)" "$rc|$(st)|$(applied)|$(compact)" "0|applied|reviewed |0"
grep -q '^\*\*Applied:\*\* 1 added, 0 changed, 0 destroyed\.$' "$T/posted" && ok "a merge without a prior apply: the usual apply comment" || bad "apply comment: $(cat "$T/posted")"

page "$P1" "$A1" "$(c 106 Bot planned "$RUN/130")" > "$T/newer.json"
go STUB_COMMENTS="$T/newer.json"
eq "a commit planned after the apply: its reviewed plan is applied, not hidden by the compact comment" "$(st)|$(applied)|$(compact)" "applied|reviewed |0"

go STUB_STALE=1 PR=
eq "no pull request number: the comments are not read and the reviewed plan is tried (as before)" "$(listed)|$(fetched)|$(posts)|$(stale)" "0|2|0|1"

page "$P1" "$(c 107 User applied "$RUN/111")" > "$T/stranger.json"
go STUB_COMMENTS="$T/stranger.json"
eq "a stranger's comment with the marker and the status applied is ignored (only the bot's comments count)" "$(st)|$(applied)|$(compact)" "applied|reviewed |0"

go STUB_STALE=1 STUB_FAIL_LIST=1
eq "the comments cannot be read: the behaviour of before (the reviewed plan is tried, then re-planned), the run does not fail" "$rc|$(st)|$(fetched)|$(stale)|$(compact)" "0|no-changes|2|1|0"

page "$P1" "$(c 108 Bot applied "$RUN/222")" > "$T/rerun.json"
go STUB_COMMENTS="$T/rerun.json"
eq "an apply comment of this very run (a re-run) was not made by /tofu apply: the behaviour of before" "$(fetched)|$(compact)" "2|0"

go STUB_STALE=1 STUB_COMMENTS="$T/planned.json"
eq "a stale reviewed plan without a prior /tofu apply is still re-planned under auto_apply (as before)" "$rc|$(st)|$(stale)|$(compact)" "0|no-changes|1|0"

go STUB_STALE=1 PLAN_MODE=fresh
eq "plan mode fresh (not the apply after a merge): the comments are not read" "$(grep -c '^GET repos/o/r/issues/7/comments' "$T/gh.log")|$(compact)" "1|0"

# the link in the compact comment only ever points at a run of this repository (else at the apply comment itself)
page "$P1" "$(c 109 Bot applied "https://evil.example/o/r/actions/runs/1")" > "$T/foreign.json"
go STUB_STALE=1 STUB_COMMENTS="$T/foreign.json"
grep -qF '([run](https://github.com/o/r/pull/7#issuecomment-109))' "$T/posted" && ! grep -q 'evil.example' "$T/posted" \
  && ok "a run link of another site in an apply comment is not copied; the compact comment links the apply comment" || bad "foreign link: $(cat "$T/posted")"

# ---- docs
grep -q '^## Why did a plan run after my apply?$' "$here/docs/runbooks.md" && ok "the runbook has the entry" || bad "runbook entry missing"
grep -q 'runbooks.md#why-did-a-plan-run-after-my-apply' "$here/docs/reference.md" && ok "the plan comments reference links to it" || bad "reference does not link the runbook entry"
grep -q 'Already applied by `/tofu apply`' "$here/docs/reference.md" && ok "the plan comments reference shows the compact comment" || bad "reference lacks the compact comment"

done_tests
