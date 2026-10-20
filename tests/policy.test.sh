#!/usr/bin/env bash
# actions/approvals/policy.jq + rules.sh: the workspace rules come from the DEFAULT BRANCH config, matched by (directory, workspace); the
# approval counts come from the repository's ruleset.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
POL="$here/actions/approvals/policy.jq"; RULES="$here/actions/approvals/rules.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
(cd "$here" && go build -o "$T/bin/tfpls" ./cmd/tfpls) || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
export PATH="$T/bin:$PATH"   # rules.sh asks `tfpls validate-config`

echo '[]' > "$T/rs-none.json"
pol() { # head-entries-json base-yaml
  local b; b=$(printf '%s' "$2" | yq -o=json '.' 2>/dev/null); { [ -n "$b" ] && [ "$b" != null ]; } || b='{}'
  jq -c --argjson base "$b" -f "$POL" <<<"$1"
}
need() { jq -r '[.rules[] | .name + "=" + .policy_packs] | join(",")' <<<"$1"; }
ent() { jq -cn --arg d "$1" --arg w "$2" --arg l "${3:-$2}" '[{name:("x/"+$w),label:$l,dir:$d,workspace:$w}]'; }

PROD='stacks:
  - name: prod
    dir: envs/prod
    workspaces: [default]
    policy_packs: [p2]
  - name: dev
    dir: envs/dev
    workspaces: [default]'
eq "the protected stack's own rule applies" "$(need "$(pol "$(ent envs/prod default prod)" "$PROD")")" "prod=p2"
eq "an alias stack on a protected directory gets main's rule (any name in the pull request)" "$(need "$(pol "$(ent envs/prod default default)" "$PROD")")" "default=p2"
eq "the same directory written another way is the same directory" "$(need "$(pol "$(ent ./envs//prod/ default)" "$PROD")")|$(need "$(pol "$(ent envs/x/../prod default)" "$PROD")")" "default=p2|default=p2"
eq "an unprotected directory has no stack rule" "$(need "$(pol "$(ent envs/dev default)" "$PROD")")" "default="

eq "a new directory gets the base defaults" "$(need "$(pol "$(ent envs/new default)" $'defaults: { policy_packs: [p3] }\n'"$PROD")")" "default=p3"
eq "base overrides apply to a new directory by workspace name" "$(need "$(pol "$(ent envs/new prod-x)" $'overrides:\n  "prod*": { policy_packs: [p4] }\n')")" "prod-x=p4"

DEF='overrides:
  "prod*": { policy_packs: [p3] }'
eq "default-layout base: a prod workspace in the default layout is protected" "$(need "$(pol "$(ent . prod-eu)" "$DEF")")" "prod-eu=p3"
eq "default-layout base while the pull request adds a stack in another directory" "$(need "$(pol "$(ent services/api prod-eu)" "$DEF")")" "prod-eu=p3"

SHARED='stacks:
  - { name: a, dir: ., workspaces: [dev] }
  - { name: b, dir: ., workspaces: [prod], policy_packs: [p3] }'
eq "two base stacks sharing a directory: each workspace gets its own" "$(need "$(pol "$(ent . dev)" "$SHARED")")|$(need "$(pol "$(ent . prod)" "$SHARED")")" "dev=|prod=p3"
eq "no config on the default branch: nothing configured (the action's strict default of 1 applies)" "$(need "$(pol "$(ent . dev)" '')")" "dev="

eq "policy settings of the apply come from main" "$(pol "$(jq -c '[.[0] + {policy_packs:""}]' <<<"$(ent envs/dev default)")" $'defaults: { policy_packs: [safety] }\n'"$PROD" | jq -r '.entries[0].policy_packs')" "safety"
eq "notify_secret comes from main too" "$(pol "$(jq -c '[.[0] + {notify_secret:"EVIL"}]' <<<"$(ent envs/prod default)")" $'defaults: { notify_secret: OPS_HOOK }\n'"$PROD" | jq -r '.entries[0].notify_secret')" "OPS_HOOK"
eq "the owner paths include main's and the pull request's" "$(pol "$(jq -c '[.[0] + {owner_paths:"mine/"}]' <<<"$(ent envs/prod default)")" "$PROD" | jq -r '[.owner_entries[].owner_paths] | join(",")')" "envs/prod,mine/"

# ---- rules.sh: reading and checking the base config
# detect-workspaces hands over an OBJECT keyed by label (what the real action outputs); `entries` must come back the same way
obj=$(jq -c '{(.[0].label): .[0]}' <<<"$(ent envs/prod default)")
: > "$T/gho"
env -i PATH="$PATH" GITHUB_OUTPUT="$T/gho" TFPLS_RULESET_FILE="$T/rs-none.json" TFPLS_BASE_CONFIG_FILE=missing HEAD_ENTRIES="$obj" CONFIG=.tfpls.yml DEFAULT_BRANCH=main REPO=o/r bash "$RULES" >/dev/null 2>&1
eq "rules.sh takes detect's object (keyed by label) and returns entries as such an object" "$(sed -n 's/^entries=//p' "$T/gho" | jq -r 'keys | join(",")')|$(sed -n 's/^rules=//p' "$T/gho" | jq -r 'type')" "default|array"
r() { env -i PATH="$PATH" TFPLS_DRY=1 TFPLS_RULESET_FILE="${RS:-$T/rs-none.json}" TFPLS_BASE_CONFIG_FILE="$1" HEAD_ENTRIES="$(ent envs/prod default)" CONFIG=.tfpls.yml DEFAULT_BRANCH=main REPO=o/r bash "$RULES" 2>&1; echo "rc=$?"; }
printf '%s\n' "$PROD" > "$T/ok.yml"
eq "rules.sh reads a base config" "$(r "$T/ok.yml" | tail -1)|$(r "$T/ok.yml" | head -1 | jq -r '.rules[0].policy_packs')" "rc=0|p2"
eq "a missing base config is fine (defaults)" "$(r missing | tail -1)" "rc=0"
printf 'defaults: { auto_apply: sometimes }\n' > "$T/bad.yml"
o=$(r "$T/bad.yml"); eq "a base config with errors refuses" "$(tail -1 <<<"$o")|$(grep -q 'config on the default branch has errors' <<<"$o" && echo yes)" "rc=1|yes"
printf 'a: [unclosed\n' > "$T/yaml.yml"
eq "a base config that is not YAML refuses" "$(r "$T/yaml.yml" | tail -1)" "rc=1"

# ---- invariant: when the pull request changes nothing, the rules equal what detect-workspaces computes
R="$T/repo"; mkdir -p "$R/workspaces/dev-eu" "$R/workspaces/prod-eu" "$R/envs/x"; touch "$R/workspaces/dev-eu/a.tf" "$R/workspaces/prod-eu/a.tf"
cat > "$R/.tfpls.yml" <<'Y'
defaults: { policy_packs: [p1] }
overrides:
  "prod-*": { approvals: { required: 2, mode: basic } }
Y
git -C "$R" init -q -b main; git -C "$R" -c user.email=t@t -c user.name=t add -A; git -C "$R" -c user.email=t@t -c user.name=t commit -qm x
: > "$T/out"
(cd "$R" && env -i PATH="$PATH" GITHUB_OUTPUT="$T/out" CONFIG=.tfpls.yml MODE=all BASE="" NAME="" tfpls detect >/dev/null 2>&1)
heads=$(sed -n 's/^entries=//p' "$T/out" | jq -c 'to_entries | map(.value)')
base=$(yq -o=json '.' "$R/.tfpls.yml")
a=$(jq -c --argjson base "$base" -f "$POL" <<<"$heads" | jq -c '[.rules[] | [.name,.policy_packs,.owner_paths]] | sort')
b=$(jq -c '[.[] | [.label,.policy_packs,.owner_paths]] | sort' <<<"$heads")
eq "head == base: the rules equal detect's policy settings and owner paths" "$a" "$b"

# ---- the approval counts come from the repository's ruleset
cnt() { RS="$1" r "$T/ok.yml" | head -1 | jq -r '[.rules[0].approvals_mode, .rules[0].approvals_required] | join(",")'; }
echo '[{"type":"pull_request","parameters":{"required_approving_review_count":2,"dismiss_stale_reviews_on_push":true}}]' > "$T/rs2.json"
echo '[{"type":"pull_request","parameters":{"required_approving_review_count":0}}]' > "$T/rs0.json"
echo '[{"type":"pull_request","parameters":{"required_approving_review_count":1,"dismiss_stale_reviews_on_push":false}},{"type":"pull_request","parameters":{"required_approving_review_count":3,"dismiss_stale_reviews_on_push":true,"require_code_owner_review":true,"require_last_push_approval":true}}]' > "$T/rs3.json"
echo '[{"type":"deletion"},{"type":"required_status_checks"}]' > "$T/rsother.json"
eq "a ruleset with 2 approvals and dismiss-stale: strict, 2" "$(cnt "$T/rs2.json")" "strict,2"
eq "a ruleset with 0 approvals: nobody has to approve" "$(cnt "$T/rs0.json")" "basic,0"
eq "several pull request rules: the strictest count, strict when any dismisses stale approvals" "$(cnt "$T/rs3.json")" "strict,3"
eq "no pull request rule: one approval of the current commit" "$(cnt "$T/rsother.json")|$(cnt "$T/rs-none.json")" "strict,1|strict,1"
g() { RS="$1" r "$T/ok.yml" >/dev/null; : > "$T/gho2"; env -i PATH="$PATH" GITHUB_OUTPUT="$T/gho2" TFPLS_RULESET_FILE="$1" TFPLS_BASE_CONFIG_FILE="$T/ok.yml" HEAD_ENTRIES="$(ent envs/prod default)" CONFIG=.tfpls.yml DEFAULT_BRANCH=main REPO=o/r bash "$RULES" >/dev/null 2>&1; grep -E '^(codeowners|last_push)=' "$T/gho2" | sed 's/^[a-z_]*=//' | tr '\n' ','; }
eq "code owner review and last-push approval are passed on" "$(g "$T/rs3.json")|$(g "$T/rs2.json")" "true,true,|false,false,"
echo 'not json' > "$T/rsbad.json"
eq "an unreadable ruleset refuses" "$(RS="$T/rsbad.json" r "$T/ok.yml" | tail -1)" "rc=1"

done_tests
