#!/usr/bin/env bash
# Tests for tools/tool-versions.sh (the weekly tool-version watch) and its workflow, with a small fake GitHub:
# releases come from fixture files, the open issues live in a JSON file the fake `gh` keeps up to date between runs.
# Run: bash tests/tool-versions.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
S="$here/tools/tool-versions.sh"
W="$here/.github/workflows/tool-versions.yml"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin" "$T/stub" "$T/root/internal/setup" "$T/root/.github/workflows"
# the script reads copies of the real files, so the test follows the real defaults and their real layout
cp "$here/internal/setup/setup.go" "$T/root/internal/setup/setup.go"
cp "$here/.github/workflows/ci.yml" "$T/root/.github/workflows/ci.yml"

cat > "$T/bin/gh" <<'STUB'
#!/usr/bin/env bash
# fake gh: `api graphql -f owner=O -f name=N ...` from rel-O_N.json; issues in issues.json (open ones only)
D=$STUB_DIR
echo "$*" >> "$D/log"
val() { local want=$1 prev='' a; shift; for a in "$@"; do [ "$prev" = "$want" ] && { printf '%s' "$a"; return 0; }; prev=$a; done; return 1; }
fval() { local want=$1 prev='' a; shift; for a in "$@"; do [ "$prev" = -f ] && [ "${a%%=*}" = "$want" ] && { printf '%s' "${a#*=}"; return 0; }; prev=$a; done; return 1; }
case "$1 $2" in
  "api graphql")
    f="$D/rel-$(fval owner "$@")_$(fval name "$@").json"
    [ -f "$f" ] || { echo "HTTP 502: Bad Gateway" >&2; exit 1; }
    cat "$f";;
  "issue list")
    [ -f "$D/fail-list" ] && { echo "HTTP 500" >&2; exit 1; }
    cat "$D/issues.json";;
  "issue create")
    n=$((100 + $(grep -c '^issue create' "$D/log")))
    jq --argjson n "$n" --arg t "$(val --title "$@")" --arg b "$(val --body "$@")" '. + [{number: $n, title: $t, body: $b}]' "$D/issues.json" > "$D/i.new" && mv "$D/i.new" "$D/issues.json"
    echo "https://github.com/o/r/issues/$n";;
  "issue edit")
    jq --argjson n "$3" --arg b "$(val --body "$@")" 'map(if .number == $n then .body = $b else . end)' "$D/issues.json" > "$D/i.new" && mv "$D/i.new" "$D/issues.json";;
  "issue close")
    val --comment "$@" > "$D/close-comment"
    jq --argjson n "$3" 'map(select(.number != $n))' "$D/issues.json" > "$D/i.new" && mv "$D/i.new" "$D/issues.json";;
  "label create") ;;
  *) echo "fake gh: unexpected call: gh $*" >&2; exit 2;;
esac
STUB
chmod +x "$T/bin/gh"
export STUB_DIR="$T/stub"

# releases <owner/repo> <tag[:pre|:draft]>...: a fixture in the order given (newest by date first, as GitHub lists them)
releases() {
  local r=$1; shift
  printf '%s\n' "$@" | jq -R --arg r "$r" 'split(":") as $p
    | {tagName: $p[0], url: ("https://github.com/" + $r + "/releases/tag/" + $p[0]), isDraft: ($p[1] == "draft"), isPrerelease: ($p[1] == "pre")}' \
    | jq -s '{data: {repository: {releases: {nodes: .}}}}' > "$STUB_DIR/rel-$(tr / _ <<<"$r").json"
}
bump() { local a b c; IFS=. read -r a b c <<<"$1"; echo "$a.$b.$((c + 1))"; }
run() { : > "$T/summary"; TFPLS_ROOT="${ROOT:-$T/root}" REPO=o/r RUN_URL=https://github.com/o/r/actions/runs/1 GH_TOKEN=x TFPLS_RETRY_DELAY=0 \
  GITHUB_STEP_SUMMARY="$T/summary" PATH="$T/bin:$PATH" bash "$S" > "$T/out" 2>&1; rc=$?; }
calls() { grep -c "^$1" "$STUB_DIR/log"; }
open_n() { jq length "$STUB_DIR/issues.json"; }
body() { jq -r '.[0].body // ""' "$STUB_DIR/issues.json"; }
fresh_log() { : > "$STUB_DIR/log"; }
hash_root() { find "$T/root" -type f -exec cksum {} + | sort; }

tofu=$(sed -nE 's/^[[:space:]]*DefaultTofu[[:space:]]*=[[:space:]]*"([0-9.]+)".*/\1/p' "$T/root/internal/setup/setup.go")
conf=$(sed -nE 's/^[[:space:]]*DefaultConftest[[:space:]]*=[[:space:]]*"([0-9.]+)".*/\1/p' "$T/root/internal/setup/setup.go")
al=$(sed -nE 's/^[[:space:]]*v=([0-9.]+); sum=.*/\1/p' "$T/root/.github/workflows/ci.yml" | head -1)
[ -n "$tofu" ] && [ -n "$conf" ] && [ -n "$al" ] && ok "the fixtures follow the real defaults (OpenTofu $tofu, conftest $conf, actionlint $al)" || bad "could not read the real defaults: '$tofu' '$conf' '$al'"
all_current() { releases opentofu/opentofu "v$tofu"; releases open-policy-agent/conftest "v$conf"; releases rhysd/actionlint "v$al"; }
before=$(hash_root)

# --- everything current and no issue: nothing to do
all_current; echo '[]' > "$STUB_DIR/issues.json"; fresh_log; run
eq "all current, no issue: exit 0, no issue created, edited or closed" "$rc|$(calls 'issue create')|$(calls 'issue edit')|$(calls 'issue close')" "0|0|0|0"
grep -q 'all current' "$T/out" && ok "it says so" || bad "output: $(cat "$T/out")"

# --- OpenTofu behind: one issue. The fixture also holds what must be ignored: an older backport released later,
# a newer pre-release, a newer draft and a tag that is not a version.
new=$(bump "$tofu")
releases opentofu/opentofu "v1.0.99" "v$new" "v$(bump "$new")-rc1:pre" "v9.9.9:draft" "nightly"
fresh_log; run
eq "OpenTofu behind: exit 0, one issue created" "$rc|$(calls 'issue create')|$(open_n)" "0|1|1"
eq "its title" "$(jq -r '.[0].title' "$STUB_DIR/issues.json")" "Tool versions behind"
grep '^issue create' "$STUB_DIR/log" | grep -q -- '--label dependencies' && ok "it carries the dependencies label" || bad "label: $(grep '^issue create' "$STUB_DIR/log")"
eq "the label is made sure of first" "$(calls 'label create dependencies')" 1
b=$(body)
grep -qF "| OpenTofu | $tofu | [$new](https://github.com/opentofu/opentofu/releases/tag/v$new) |" <<<"$b" && ok "the row names the default and links the latest release" || bad "row: $b"
eq "the highest published version wins: no pre-release, no draft, no older backport, no non-version tag" "$(grep -c '9\.9\.9\|rc1\|1\.0\.99\|nightly' <<<"$b")" 0
eq "only the tool that is behind is listed" "$(grep -c '| conftest |\|| actionlint |' <<<"$b")" 0
grep -q 'internal/setup/setup.go' <<<"$b" && ok "the body says where the default lives" || bad "where: $b"
grep -q 'OpenTofu' "$T/summary" && ok "the run summary has the table too" || bad "summary: $(cat "$T/summary")"

# --- the same again: no duplicate, no edit
fresh_log; run
eq "second run, same list: no second issue, no edit" "$rc|$(calls 'issue create')|$(calls 'issue edit')|$(open_n)" "0|0|0|1"
grep -q 'unchanged' "$T/out" && ok "it says the issue is unchanged" || bad "output: $(cat "$T/out")"

# --- the list changes (actionlint behind too): the issue is edited, not duplicated
newal=$(bump "$al"); releases rhysd/actionlint "v$newal"
fresh_log; run
eq "list changed: the open issue is edited, not duplicated" "$rc|$(calls 'issue create')|$(calls 'issue edit 101')|$(open_n)" "0|0|1|1"
b=$(body)
{ grep -qF "| OpenTofu | $tofu |" <<<"$b" && grep -qF "| actionlint | $al | [$newal](" <<<"$b"; } && ok "the edited body lists both tools" || bad "body: $b"
grep -q 'sum=' <<<"$b" && ok "the actionlint row says the checksum must change with it" || bad "actionlint hint: $b"

# --- everything current again: the issue is closed with a comment
all_current; fresh_log; run
eq "all current again: the issue is closed" "$rc|$(calls 'issue close 101')|$(open_n)" "0|1|0"
c=$(cat "$STUB_DIR/close-comment")
{ grep -q "OpenTofu $tofu" <<<"$c" && grep -q "conftest $conf" <<<"$c" && grep -q "actionlint $al" <<<"$c" && grep -q 'https://github.com/o/r/actions/runs/1' <<<"$c"; } && ok "the closing comment names the versions and links the run" || bad "comment: $c"

# --- current and no issue again: nothing at all
fresh_log; run
eq "current, no issue: only reads (no create, edit, close or label)" "$rc|$(calls 'issue create')|$(calls 'issue edit')|$(calls 'issue close')|$(calls 'label')" "0|0|0|0|0"

# --- an open issue with that exact title opened by hand is adopted, one with another title is left alone
echo '[{"number":7,"title":"Tool versions behind","body":"old text"},{"number":8,"title":"Tool versions behind (notes)","body":"x"}]' > "$STUB_DIR/issues.json"
releases opentofu/opentofu "v$new"; fresh_log; run
eq "an existing issue with the exact title is updated instead of opening another" "$(calls 'issue create')|$(calls 'issue edit 7')|$(calls 'issue edit 8')" "0|1|0"
all_current; fresh_log; run
eq "and closed when current; the other issue is untouched" "$(calls 'issue close 7')|$(calls 'issue close 8')|$(jq -r '.[0].number' "$STUB_DIR/issues.json")" "1|0|8"

# --- failures: never reported as current, nothing closed or opened
echo '[{"number":9,"title":"Tool versions behind","body":"old"}]' > "$STUB_DIR/issues.json"
rm "$STUB_DIR/rel-rhysd_actionlint.json"; fresh_log; run
eq "a release list that cannot be read fails the run and closes nothing" "$rc|$(calls 'issue close')|$(calls 'issue create')|$(open_n)" "1|0|0|1"
grep -q '::error::.*rhysd/actionlint' "$T/out" && ok "the error names the repository" || bad "error: $(cat "$T/out")"
eq "a failing call is tried three times first" "$(grep -c '^api graphql .*name=actionlint' "$STUB_DIR/log")" 3
releases rhysd/actionlint "v1.0.0-beta1:pre"; fresh_log; run
eq "no published release at all fails too" "$rc|$(calls 'issue close')" "1|0"
all_current; touch "$STUB_DIR/fail-list"; fresh_log; run
eq "the open issues cannot be listed: fail, open nothing" "$rc|$(calls 'issue create')|$(calls 'issue close')" "1|0|0"
rm -f "$STUB_DIR/fail-list"
mkdir -p "$T/broken/internal/setup" "$T/broken/.github/workflows"
sed 's/^[[:space:]]*DefaultTofu[[:space:]]*=.*/DefaultTofu = os.Getenv("X")/' "$T/root/internal/setup/setup.go" > "$T/broken/internal/setup/setup.go"
cp "$T/root/.github/workflows/ci.yml" "$T/broken/.github/workflows/ci.yml"
fresh_log; ROOT="$T/broken" run
eq "a default that cannot be read fails the run and changes no issue" "$rc|$(calls 'issue')" "1|0"
grep -q '::error::.*internal/setup/setup.go' "$T/out" && ok "the error names the file" || bad "error: $(cat "$T/out")"

# --- it never edits a file
eq "no file under the checked tree changed in any run" "$(hash_root)" "$before"
grep -nE '(^|[^&0-9])>>?[[:space:]]*"?\$(root|TFPLS_ROOT)' "$S" >/dev/null 2>&1 && bad "the script writes into the tree it checks" || ok "the script has no redirection into the tree it checks"

# --- the workflow: weekly and by hand, least privilege, runs the script
yq '.' "$W" >/dev/null 2>&1 && ok "tool-versions.yml parses" || bad "tool-versions.yml missing or invalid"
eq "triggers: a weekly schedule and workflow_dispatch" "$(yq -r '.on | keys | sort | join(",")' "$W" 2>/dev/null)|$(yq -r '.on.schedule | length' "$W" 2>/dev/null)" "schedule,workflow_dispatch|1"
eq "permissions: contents read and issues write, nothing else" "$(yq -r '.permissions | to_entries | map(.key + ":" + .value) | sort | join(",")' "$W" 2>/dev/null)" "contents:read,issues:write"
eq "no job widens them" "$(yq -r '[.jobs[] | select(has("permissions"))] | length' "$W" 2>/dev/null)" 0
grep -q 'bash tools/tool-versions.sh' "$W" && ok "the job runs tools/tool-versions.sh" || bad "script not run"
eq "the checkout keeps no token" "$(grep -c 'uses: actions/checkout@' "$W")|$(grep -c 'persist-credentials: false' "$W")" "1|1"

done_tests
