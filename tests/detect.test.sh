#!/usr/bin/env bash
# Tests for `tfpls detect` (internal/detect, run by actions/detect-workspaces) against a temp git repo that has the
# layout of a "one root module, many tofu workspaces" repo. Needs git, jq, yq, go. Run: bash tests/detect.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
build_bin tfpls "$T/tfpls" || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
ACTION="$here/../actions/detect-workspaces/action.yml"
[ "$(yq -r '.runs.steps[0].uses' "$ACTION")|$(yq -r '.runs.steps[1].run' "$ACTION")" = "denzhel/tfpls/actions/bin@main|tfpls detect" ] && ok "the action installs the binary and runs tfpls detect" || bad "the action does not run tfpls detect"

gt() { git -C "$R" -c user.email=t@t -c user.name=t -c commit.gpgsign=false "$@"; }
R="$T/repo"; mkdir -p "$R"; gt init -q -b main
for w in dev-us-east-1 stg-us-west-2 prod-us-west-1 dr-us-east-1; do mkdir -p "$R/workspaces/$w"; echo "# $w" > "$R/workspaces/$w/main.tf"; done
echo "# root" > "$R/provider.tf"; echo "# wsmain" > "$R/workspaces/main.tf"; echo "x" > "$R/README.md"; mkdir "$R/docs"; echo x > "$R/docs/a.md"
cat > "$R/workspaces.txt" <<'L'
# listed workspaces
alpha
beta   # trailing comment
L
mkdir -p "$R/other/alpha" "$R/other/beta"; echo x > "$R/other/alpha/a.tf"; echo x > "$R/other/beta/b.tf"
cat > "$R/.tfpls.yml" <<'Y'
defaults: { auto_apply: never }
stacks:
  - name: terraform
    dir: .
    workspaces_glob: "workspaces/*"
    workspace_paths: ["workspaces/{workspace}/"]
    ignore: ["**/*.md", "docs/**"]
    triggers:
      prod-us-west-1: [dr-us-east-1]
    overrides:
      "dev-*":  { auto_apply: always }
      "stg-*":  { auto_apply: additive_only }
      "prod-*": { auto_apply: never, require_reviewed_plan: true }
Y
gt add -A >/dev/null; gt commit -qm base; BASE0=$(gt rev-parse HEAD)

# run <config> <name selectors> <base>   -> sets rc, OUT (stderr+stdout, kept for debugging), and $T/out with the outputs
# shellcheck disable=SC2034
run() {
  : > "$T/out"
  OUT=$(cd "$R" && env -i PATH="$PATH" GITHUB_OUTPUT="$T/out" CONFIG="$1" MODE=changed BASE="${3:-}" HEAD_REF="${4:-}" NAME="${2:-}" "$T/tfpls" detect 2>&1); rc=$?
}
wss() { sed -n 's/^entries=//p' "$T/out" | jq -r '[.[].workspace] | sort | join(",")'; }
field() { sed -n 's/^entries=//p' "$T/out" | jq -r --arg w "$1" --arg f "$2" '.[] | select(.workspace==$w) | .[$f]'; }
commit_change() { echo "# change $RANDOM" >> "$R/$1"; gt add -A >/dev/null; gt commit -qm "change $1"; }
eq() { [ "$2" = "$3" ] && ok "$1" || bad "$1 (got '$2', want '$3')"; }

ALL="dev-us-east-1,dr-us-east-1,prod-us-west-1,stg-us-west-2"

# output shape: the matrix has ONE key (label) so jobs need no custom name; details are looked up in `entries`
run .tfpls.yml "" ""
eq "matrix has a single key: label" "$(sed -n 's/^matrix=//p' "$T/out" | jq -c '[.include[] | keys] | unique')" '[["label"]]'
eq "matrix labels are the workspace names" "$(sed -n 's/^matrix=//p' "$T/out" | jq -r '[.include[].label] | sort | join(",")')" "$ALL"
eq "entries are keyed by label" "$(sed -n 's/^entries=//p' "$T/out" | jq -r 'keys | join(",")')" "$ALL"
eq "an entry carries what the jobs need" "$(sed -n 's/^entries=//p' "$T/out" | jq -r '.["dev-us-east-1"] | [.name,.dir,.workspace,.auto_apply] | join("|")')" "terraform/dev-us-east-1|.|dev-us-east-1|always"
run .tfpls.yml "" "";                     eq "no base => every workspace" "$(wss)" "$ALL"
run .tfpls.yml "" 0000000000000000000000000000000000000000; eq "all-zero base (new branch) => every workspace" "$(wss)" "$ALL"
run .tfpls.yml "" deadbeefdeadbeefdeadbeefdeadbeefdeadbeef; eq "unknown base => every workspace" "$(wss)" "$ALL"

commit_change workspaces/dev-us-east-1/main.tf
run .tfpls.yml "" "$BASE0";               eq "change in one workspace dir selects only it" "$(wss)" "dev-us-east-1"
commit_change workspaces/stg-us-west-2/main.tf
run .tfpls.yml "" "$BASE0";               eq "two workspaces" "$(wss)" "dev-us-east-1,stg-us-west-2"
B1=$(gt rev-parse HEAD)
commit_change provider.tf
run .tfpls.yml "" "$B1";                  eq "shared root file selects every workspace" "$(wss)" "$ALL"
commit_change workspaces/main.tf
run .tfpls.yml "" "$(gt rev-parse HEAD~1)"; eq "workspaces/main.tf (shared) selects every workspace" "$(wss)" "$ALL"
B2=$(gt rev-parse HEAD)
commit_change README.md; commit_change docs/a.md
run .tfpls.yml "" "$B2";                  eq "ignored files select nothing" "$(wss)" ""
grep -q '^any=false' "$T/out" && ok "any=false when nothing selected" || bad "any=false"

# a file moved between workspaces selects BOTH (git lists only the destination of a rename unless told not to)
BR=$(gt rev-parse HEAD)
gt mv workspaces/stg-us-west-2/main.tf workspaces/dev-us-east-1/moved.tf; gt commit -qm move
run .tfpls.yml "" "$BR";                  eq "a moved file selects the workspace it left as well" "$(wss)" "dev-us-east-1,stg-us-west-2"
gt mv workspaces/dev-us-east-1/moved.tf workspaces/stg-us-west-2/main.tf; gt commit -qm back

B3=$(gt rev-parse HEAD)
commit_change workspaces/prod-us-west-1/main.tf
run .tfpls.yml "" "$B3";                  eq "triggers: prod change also selects its DR twin" "$(wss)" "dr-us-east-1,prod-us-west-1"

run .tfpls.yml "prod-*" "";               eq "glob selection" "$(wss)" "prod-us-west-1"
run .tfpls.yml "dr-us-east-1 terraform/stg-us-west-2" ""; eq "name and stack/name selection" "$(wss)" "dr-us-east-1,stg-us-west-2"
run .tfpls.yml "prod-us-west-1" "$B3";    eq "explicit selection ignores triggers" "$(wss)" "prod-us-west-1"
run .tfpls.yml "nope" "";                 eq "unknown name selects nothing" "$(wss)" ""
run .tfpls.yml "all" "";                  eq "'all' selects every workspace" "$(wss)" "$ALL"

run .tfpls.yml "" "";
eq "override: dev auto_apply" "$(field dev-us-east-1 auto_apply)" "always"
eq "override: stg auto_apply" "$(field stg-us-west-2 auto_apply)" "additive_only"
eq "override: prod require_reviewed_plan" "$(field prod-us-west-1 require_reviewed_plan)" "true"
eq "default require_reviewed_plan is false" "$(field dev-us-east-1 require_reviewed_plan)" "false"
eq "label is the workspace name" "$(field dev-us-east-1 label)" "dev-us-east-1"
eq "stack-qualified name is kept" "$(field dev-us-east-1 name)" "terraform/dev-us-east-1"

# the glob semantics used by `ignore` (gitignore-like) are unit-tested in internal/detect/glob_test.go

# lock settings: defaults, overrides and validation
cat > "$R/lock.yml" <<'Y'
stacks:
  - name: s
    dir: .
    workspaces: [a, b]
    overrides:
      a: { plan_lock: true, lock_timeout: 5m }
Y
run lock.yml "" ""
eq "plan_lock defaults to false: a pull request plan never blocks an apply" "$(field b plan_lock)" "false"
eq "lock_timeout defaults to 2m: a held lock is waited for, not an instant failure" "$(field b lock_timeout)" "2m"
eq "override: plan_lock true" "$(field a plan_lock)" "true"
eq "override: lock_timeout 5m" "$(field a lock_timeout)" "5m"
cat > "$R/lock-bad.yml" <<'Y'
stacks: [{ name: s, dir: ., workspaces: [a], lock_timeout: "soon" }]
Y
run lock-bad.yml "" "";  [ $rc -ne 0 ] && ok "invalid lock_timeout is rejected" || bad "invalid lock_timeout"
cat > "$R/lock-bad2.yml" <<'Y'
stacks: [{ name: s, dir: ., workspaces: [a], plan_lock: maybe }]
Y
run lock-bad2.yml "" "";  [ $rc -ne 0 ] && ok "invalid plan_lock is rejected" || bad "invalid plan_lock"

# matrix size guard (GitHub allows at most 256 jobs per matrix)
run() { : > "$T/out"; OUT=$(cd "$R" && env -i PATH="$PATH" GITHUB_OUTPUT="$T/out" CONFIG="$1" MODE=changed BASE="${3:-}" HEAD_REF="${4:-}" NAME="${2:-}" TFPLS_MAX_MATRIX="${TFPLS_MAX_MATRIX:-256}" "$T/tfpls" detect 2>&1); rc=$?; }
TFPLS_MAX_MATRIX=3 run .tfpls.yml "" ""
[ $rc -ne 0 ] && ok "more workspaces than the matrix limit => error" || bad "limit not enforced"
case "$OUT" in *"selects 4 workspaces"*"at most 3 jobs"*"Split the change"*) ok "the error says how many were selected, the limit and what to do";; *) bad "message: $OUT";; esac
TFPLS_MAX_MATRIX=4 run .tfpls.yml "" ""
[ $rc -eq 0 ] && ok "exactly at the limit is fine" || bad "at the limit: $OUT"
TFPLS_MAX_MATRIX=3 run .tfpls.yml "dev-us-east-1" ""
[ $rc -eq 0 ] && ok "a smaller explicit selection stays under the limit" || bad "explicit selection: $OUT"
TFPLS_MAX_MATRIX=256 run .tfpls.yml "" ""

# config problems are reported by detection: a typo warns (the run goes on), a wrong value stops it
cat > "$R/typo.yml" <<'Y'
stacks:
  - name: s
    workspaces: [a]
    autoapply: always
Y
run typo.yml "" ""
[ $rc -eq 0 ] && ok "a typo in a key does not stop the run" || bad "typo stopped the run: $OUT"
case "$OUT" in *"::warning::stacks[0]: unknown key \`autoapply\` is ignored (did you mean \`auto_apply\`?)"*) ok "the typo is reported as a warning with a hint";; *) bad "typo warning: $OUT";; esac
cat > "$R/wrongval.yml" <<'Y'
stacks:
  - name: s
    workspaces: [a]
    overrides: { "x*": { auto_apply: yes, lock_timeout: 5 } }
Y
run wrongval.yml "" ""
[ $rc -ne 0 ] && ok "a wrong value stops the run" || bad "wrong value accepted"
case "$OUT" in *"::error::stacks[0].overrides[\"x*\"].auto_apply: must be never, always or additive_only"*"::error::stacks[0].overrides[\"x*\"].lock_timeout"*) ok "every error is listed with its path";; *) bad "error list: $OUT";; esac

# workspaces_file with comments, and label disambiguation between two stacks sharing workspace names
cat > "$R/two.yml" <<'Y'
stacks:
  - name: one
    dir: other
    workspaces_file: workspaces.txt
  - name: two
    dir: other
    workspaces: [alpha]
Y
run two.yml "" ""
eq "workspaces_file ignores comments/blank lines (+ inline list)" "$(sed -n 's/^entries=//p' "$T/out" | jq -r '[.[].name] | sort | join(",")')" "one/alpha,one/beta,two/alpha"
eq "shared workspace name => label falls back to stack/workspace" "$(sed -n 's/^entries=//p' "$T/out" | jq -r '.[] | select(.name=="two/alpha") | .label')" "two/alpha"
eq "unique workspace name keeps the short label" "$(sed -n 's/^entries=//p' "$T/out" | jq -r '.[] | select(.name=="one/beta") | .label')" "beta"

# after: ordering between workspaces of one run
cat > "$R/after.yml" <<'Y'
stacks:
  - name: terraform
    dir: .
    workspaces_glob: "workspaces/*"
    overrides:
      "dr-*": { after: ["prod-*"] }
      "prod-*": { after: ["stg-*"] }
Y
run after.yml "" ""
eq "after: predecessors come first in the matrix" "$(sed -n 's/^matrix=//p' "$T/out" | jq -r '[.include[].label] | join(",")' | tr ',' '\n' | grep -nE 'stg|prod|dr-' | cut -d: -f2 | tr '\n' ',')" "stg-us-west-2,prod-us-west-1,dr-us-east-1,"
eq "after: wait_for lists the resolved predecessor labels" "$(field dr-us-east-1 wait_for)" "prod-us-west-1"
eq "after: a workspace with no predecessors waits for nothing" "$(field dev-us-east-1 wait_for)" ""
eq "after: chains resolve" "$(field prod-us-west-1 wait_for)" "stg-us-west-2"
run after.yml "dr-us-east-1" ""
eq "after: a predecessor that is not part of this run is not waited for" "$(field dr-us-east-1 wait_for)" ""
cat > "$R/cycle.yml" <<'Y'
stacks:
  - name: terraform
    dir: .
    workspaces_glob: "workspaces/*"
    overrides:
      "dr-*": { after: ["prod-*"] }
      "prod-*": { after: ["dr-*"] }
Y
run cycle.yml "" ""
eq "after: a cycle is an error that names the workspaces" "$([ "$rc" -ne 0 ] && echo failed)|$(grep -c 'cycle' <<<"$OUT")" "failed|1"

run .tfpls.yml "dr-us-east-1" ""
eq "owner_paths: the workspace directory used for CODEOWNERS lookups" "$(field dr-us-east-1 owner_paths)" "workspaces/dr-us-east-1/"

# zero config: no file at all, or only overrides
echo "# shared" >> "$R/README.md"; gt add -A >/dev/null; gt commit -qm readme
run nonexistent.yml "" ""
eq "no config file: one workspace per directory of workspaces/*" "$(wss)" "$ALL"
eq "no config file: nothing auto-applies" "$(field dev-us-east-1 auto_apply)" never
eq "no config file: a notice says defaults are used" "$(grep -c 'using the defaults' <<<"$OUT")" 1
commit_change workspaces/dev-us-east-1/main.tf; run nonexistent.yml "" "$(gt rev-parse HEAD~1)"
eq "no config file: a change inside one workspace selects only it" "$(wss)" "dev-us-east-1"
echo "x" >> "$R/README.md"; gt add -A >/dev/null; gt commit -qm doc; run nonexistent.yml "" "$(gt rev-parse HEAD~1)"
eq "default stack ignores documentation changes" "$(wss)" ""
cat > "$R/ov.yml" <<'Y'
overrides:
  "dev-*":  { auto_apply: always }
  "prod-*": { policy_packs: [safety] }
Y
run ov.yml "" ""
eq "only overrides: the default stack with the overrides applied" "$(field dev-us-east-1 auto_apply)|$(field prod-us-west-1 policy_packs)|$(field stg-us-west-2 auto_apply)" "always|safety|never"
cat > "$R/ov2.yml" <<'Y'
overrides:
  "dev-*": { auto_apply: always }
stacks:
  - name: terraform
    dir: .
    workspaces_glob: "workspaces/*"
    overrides:
      "dev-*": { auto_apply: additive_only }
      "stg-*": { auto_apply: always }
Y
run ov2.yml "" ""
eq "top-level overrides apply to explicit stacks and the stack's own overrides win" "$(field dev-us-east-1 auto_apply)|$(field stg-us-west-2 auto_apply)" "additive_only|always"

# validation
cat > "$R/bad1.yml" <<'Y'
stacks: [{ name: s, dir: ., workspaces: [a], auto_apply: sometimes }]
Y
run bad1.yml "" "";  [ $rc -ne 0 ] && ok "invalid auto_apply is rejected" || bad "invalid auto_apply"
cat > "$R/bad2.yml" <<'Y'
stacks: [{ name: s, dir: ., workspaces: ["a b;rm"] }]
Y
run bad2.yml "" "";  [ $rc -ne 0 ] && ok "unsafe workspace name is rejected" || bad "unsafe workspace name"
cat > "$R/bad3.yml" <<'Y'
stacks: [{ name: s, dir: ., workspaces: [a], triggers: { a: "b" } }]
Y
run bad3.yml "" "";  [ $rc -ne 0 ] && ok "malformed triggers are rejected" || bad "malformed triggers"
cat > "$R/dup.yml" <<'Y'
stacks:
  - { name: s, dir: ., workspaces: [a] }
  - { name: s, dir: ., workspaces: [a] }
Y
run dup.yml "" "";   [ $rc -ne 0 ] && ok "duplicate stack/workspace names are rejected" || bad "duplicates"

# notify_template reaches the entry
cat > "$R/nt.yml" <<'Y'
stacks: [{ name: s, dir: ., workspaces: [a, b], overrides: { a: { notify_template: .github/tfpls/slack.jq } } }]
Y
run nt.yml "" ""
eq "notify_template is carried into the entry (empty when unset)" "$(sed -n 's/^entries=//p' "$T/out" | jq -r '[.[]] | sort_by(.workspace) | map(.notify_template) | join("|")')" ".github/tfpls/slack.jq|"

# drift_fix: on by default, off per workspace
cat > "$R/df.yml" <<'Y'
stacks: [{ name: s, dir: ., workspaces: [a, b], overrides: { b: { drift_fix: false } } }]
Y
run df.yml "" ""
eq "drift_fix defaults to true and can be switched off per workspace" "$(sed -n 's/^entries=//p' "$T/out" | jq -r '[.[]] | sort_by(.workspace) | map(.drift_fix) | join("|")')" "true|false"

# ---- first-run friendliness: labels, overrides by stack name, warnings
mkdir -p "$R/envs/dev" "$R/envs/prod"; echo x > "$R/envs/dev/a.tf"; echo x > "$R/envs/prod/a.tf"
cat > "$R/envs.yml" <<'Y'
stacks:
  - { name: dev,  dir: envs/dev,  workspaces: [default] }
  - { name: prod, dir: envs/prod, workspaces: [default], watch: [modules] }
overrides:
  "prod*": { auto_apply: never, drift_fix: false }
  "nothing-*": { auto_apply: always }
Y
run envs.yml "" ""
eq "separate root modules with the tofu workspace default: the label is the stack name" "$(sed -n 's/^matrix=//p' "$T/out" | jq -r '[.include[].label] | sort | join(",")')" "dev,prod"
eq "an override pattern also matches the stack name" "$(sed -n 's/^entries=//p' "$T/out" | jq -r '.prod.drift_fix')" "false"
eq "configured counts every workspace, changed or not" "$(sed -n 's/^configured=//p' "$T/out")" "2"
case "$OUT" in *'override "nothing-*" matches no workspace and no stack name'*) ok "an override that matches nothing is a warning";; *) bad "no unmatched-override warning: $OUT";; esac
run envs.yml "dev" ""
eq "a stack is selected by its label, and the label does not change with the selection" "$(sed -n 's/^matrix=//p' "$T/out" | jq -r '[.include[].label] | join(",")')" "dev"
printf 'stacks: [{ name: s, dir: ., workspaces: [] }]\n' > "$R/empty.yml"; run empty.yml "" ""
case "$OUT" in *"no workspaces found"*) ok "no workspaces at all is a warning that says how to describe the layout";; *) bad "no-workspaces warning missing: $OUT";; esac
eq "and configured is 0" "$(sed -n 's/^configured=//p' "$T/out")" "0"

# a base older than the base of the merge ref (the base branch moved after the pull request was opened): with the pull request's
# own head only its own changes select workspaces; without it the diff keeps its old meaning. A fresh repository, default layout.
R="$T/race"; mkdir -p "$R"; gt init -q -b main
for w in dev stg qa; do mkdir -p "$R/workspaces/$w"; echo "# $w" > "$R/workspaces/$w/main.tf"; done
gt add -A >/dev/null; gt commit -qm base; M0=$(gt rev-parse HEAD)
gt switch -q -c pr; commit_change workspaces/dev/main.tf; commit_change workspaces/stg/main.tf; PRHEAD=$(gt rev-parse HEAD)
gt switch -q main; commit_change workspaces/qa/main.tf     # the base branch moves on: not part of the pull request
gt merge -q --no-ff -m "merge ref" pr >/dev/null             # what the merge ref of the pull request looks like
run none.yml "" "$M0";            eq "stale base, no head_ref: what the base branch gained is counted too (unchanged default)" "$(wss)" "dev,qa,stg"
run none.yml "" "$M0" "$PRHEAD";  eq "stale base with head_ref: only the pull request's own changes select, both of its workspaces" "$(wss)" "dev,stg"
eq "plan.yml gives the detect step the pull request's own head" "$(yq -r '.jobs.detect.steps[] | select(.id == "d") | .with.head_ref' "$here/../.github/workflows/plan.yml")" '${{ github.event.pull_request.head.sha }}'

done_tests
