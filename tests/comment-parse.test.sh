#!/usr/bin/env bash
# Runs the real `tfpls comment parse` (the step of comment-ops.yml) against a stubbed `gh`: which /tofu commands are accepted,
# and that every rejected command is explained to the user in a PR comment. Run: bash tests/comment-parse.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
(cd "$here" && go build -o "$T/tfpls" ./cmd/tfpls) || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
CW="$here/.github/workflows/comment-ops.yml"
eq "comment-ops.yml installs the binary, then runs tfpls comment parse" "$(yq -r '[.jobs.parse.steps[0].uses, (.jobs.parse.steps[] | select(.id == "p") | .run)] | join("|")' "$CW")" "denzhel/tfpls/actions/bin@main|tfpls comment parse"

mkdir "$T/bin"
cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
# minimal gh stub: canned answers, records PR comments and reactions
args="$*"
DEFAULT_REVIEWS='[{"user":{"login":"rev1"},"state":"APPROVED","commit_id":"abc123","submitted_at":"2026-01-01T00:00:00Z"}]'
jqexpr=""; prev=""
for a in "$@"; do [ "$prev" = --jq ] && jqexpr=$a; prev=$a; done
emit() { if [ -n "$jqexpr" ]; then printf '%s' "$1" | jq -r "$jqexpr"; else printf '%s\n' "$1"; fi; }
case "$1 $2" in
  "pr comment") shift 2; while [ $# -gt 0 ]; do [ "$1" = --body ] && printf '%s\n' "$2" >> "$STUB_DIR/comments"; shift; done; exit 0;;
esac
case "$args" in
  *permission*)   emit "{\"permission\":\"${STUB_PERM:-write}\"}";;
  *reactions*)    echo "$args" >> "$STUB_DIR/reactions"; echo '{}';;
  *compare*)      emit "{\"behind_by\":${STUB_BEHIND:-0}}";;
  *"/reviews"*)   printf '%s' "${STUB_REVIEWS:-$DEFAULT_REVIEWS}";;
  *"/pulls/"*)    emit "{\"state\":\"${STUB_PRSTATE:-open}\",\"head\":{\"sha\":\"abc123\",\"repo\":{\"full_name\":\"${STUB_HEADREPO:-o/r}\"}},\"base\":{\"ref\":\"main\",\"sha\":\"def456\"}}";;
  *) echo '{}';;
esac
STUB
chmod +x "$T/bin/gh"

# run <comment body> [VAR=value ...]  -> rc, OUTF (step outputs), CMT (comments posted), RX (reactions)
run() {
  local body=$1; shift
  rm -f "$T/comments" "$T/reactions"; : > "$T/out"
  env -i PATH="$T/bin:$PATH" STUB_DIR="$T" GITHUB_OUTPUT="$T/out" GITHUB_REPOSITORY=o/r GH_TOKEN=x \
      BODY="$body" ACTOR=u PR=7 COMMENT_ID=99 REQUIRE_APPROVAL=true REQUIRE_UTD=true "$@" "$T/tfpls" comment parse >/dev/null 2>&1
  rc=$?
  OUTF=$(cat "$T/out"); CMT=$(cat "$T/comments" 2>/dev/null || true); RX=$(cat "$T/reactions" 2>/dev/null || true)
}
accepted() { # name body [env...]
  local n=$1 b=$2; shift 2; run "$b" "$@"
  [ $rc -eq 0 ] && [ -z "$CMT" ] && ok "accepted: $n" || bad "should be accepted: $n (rc=$rc, comment: $CMT)"
}
rejected() { # name body expected-text [env...]
  local n=$1 b=$2 want=$3; shift 3; run "$b" "$@"
  if [ $rc -ne 0 ] && [[ "$CMT" == *"$want"* ]] && [[ "$RX" == *confused* ]]; then ok "rejected with a PR reply: $n"; else bad "$n (rc=$rc, comment: '$CMT', reactions: '$RX')"; fi
}

accepted "plan with no args"            '/tofu plan'
accepted "plan two names"               '/tofu plan dev-us-east-1 stg-us-west-2'
accepted "plan glob"                    '/tofu plan prod-*'
accepted "plan all"                     '/tofu plan all'
accepted "apply one workspace"          '/tofu apply dev-us-east-1'
accepted "apply with one -target"       '/tofu apply dev-us-east-1 -target=aws_sqs_queue.this["jobs"]'
accepted "unlock one workspace"         '/tofu unlock dev-us-east-1'
case "$OUTF" in *"cmd=unlock"*"names=dev-us-east-1"*) ok "outputs carry cmd and names";; *) bad "outputs: $OUTF";; esac

rejected "unknown subcommand"           '/tofu bogus'                          "unknown command"
rejected "illegal characters in name"   '/tofu plan a;b'                       "unexpected argument"
rejected "apply with no workspace"      '/tofu apply'                          "apply needs explicit workspace"
rejected "-target with a glob"          '/tofu apply prod-* -target=a.b'       "-target requires exactly one workspace"
rejected "-target with two workspaces"  '/tofu apply a b -target=x.y'          "-target requires exactly one workspace"
rejected "-target without workspace"    '/tofu apply -target=x.y'              "-target requires exactly one workspace"
rejected "unlock with a glob"           '/tofu unlock dev-*'                   "usage: /tofu unlock"
rejected "unlock with two names"        '/tofu unlock a b'                     "usage: /tofu unlock"
rejected "unlock with -target"          '/tofu unlock a -target=x.y'           "usage: /tofu unlock"
rejected "commenter without write access" '/tofu plan'                         "lacks write access"  STUB_PERM=read
rejected "pull request from a fork"     '/tofu plan'                           "fork PRs are not supported"  STUB_HEADREPO=other/fork
rejected "closed pull request"           '/tofu plan'                           "not open"  STUB_PRSTATE=closed
# approval rules (modes, counts, stale approvals) moved to actions/approvals and are tested in tests/approvals.test.sh
NONE='[]'
rejected "apply while behind the base"  '/tofu apply dev'                      "commits behind"              STUB_BEHIND=3
accepted "plan is allowed without approval or up to date" '/tofu plan dev'  STUB_REVIEWS="$NONE" STUB_BEHIND=3
accepted "unlock needs no approval" '/tofu unlock dev'  STUB_REVIEWS="$NONE"
accepted "apply allowed when approval and branch checks are off" '/tofu apply dev' STUB_REVIEWS="$NONE" STUB_BEHIND=3 REQUIRE_APPROVAL=false REQUIRE_UTD=false
# a backtick in the comment must not break the reply formatting
run '/tofu bog`us' ; case "$CMT" in *'`us'*|*'bog`'*) bad "backtick kept in the reply";; *) ok "backticks are stripped from the reply";; esac
run '/tofu unlock a b'; case "$CMT" in *'&lt;workspace&gt;'*) ok "< and > are escaped so GitHub does not hide <workspace>";; *) bad "angle brackets not escaped: $CMT";; esac

# --now: admins only, only with apply
run "/tofu apply dev --now" STUB_PERM=admin; eq "--now by an admin is accepted" "$rc|$(grep -c '^now=true' <<<"$OUTF")" "0|1"
eq "and announced on the pull request" "$(grep -c -- '--now' <<<"$CMT")" 1
run "/tofu apply dev --now" STUB_PERM=write; eq "--now by a writer is refused" "$rc|$(grep -c 'only repository admins' <<<"$CMT")" "1|1"
run "/tofu plan dev --now" STUB_PERM=admin; eq "--now with plan is refused" "$rc|$(grep -c 'only goes with /tofu apply' <<<"$CMT")" "1|1"
run "/tofu apply dev" STUB_PERM=admin; eq "without --now the flag is false" "$(grep -c '^now=false' <<<"$OUTF")" 1

# no step of a workflow or action calls the jq binary (gh's own --jq is fine); the glue steps are single tfpls commands
n=0
for f in "$here"/.github/workflows/*.yml "$here"/actions/*/action.yml; do
  yq -r '.. | select(type == "object" and has("run")) | .run' "$f" 2>/dev/null | grep -Eq '(^|[|;&(]|\$\()[[:space:]]*jq[[:space:]]' && { bad "$f invokes jq"; n=1; }
done
[ $n -eq 0 ] && ok "no workflow or action step invokes the jq binary"
eq "the nothing-matched step is tfpls comment nomatch" "$(yq -r '.jobs.parse.steps[] | select(.name == "tell the user when nothing matched") | .run' "$CW")" "tfpls comment nomatch"
W="$here/.github/workflows"
eq "dashboard, drift, drift-fix and apply run one tfpls glue command each" "$(yq -r '.jobs[].steps[] | select(.run != null and (.run | test("^tfpls glue "))) | .run' "$W/dashboard.yml" "$W/drift.yml" "$W/drift-fix.yml" "$W/apply.yml" | grep -v -- '^---$' | tr '\n' ',')" "tfpls glue store-page,tfpls glue drift-state,tfpls glue drift-issue,tfpls glue merged-pr,"

done_tests
