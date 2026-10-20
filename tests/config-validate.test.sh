#!/usr/bin/env bash
# Config validation (actions/detect-workspaces/validate.jq): every mistake gets an actionable message with its path,
# unknown keys are warnings with a "did you mean" hint, valid configs are clean. Run: bash tests/config-validate.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
V="$here/actions/detect-workspaces/validate.jq"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# check <name> <yaml> <level> <text that must appear in a message of that level>   (use level "none" for a clean config)
check() {
  local name=$1 yaml=$2 level=$3 want=${4:-} out
  out=$(printf '%s\n' "$yaml" | yq -o=json '.' | jq -c -f "$V")
  if [ "$level" = none ]; then
    [ "$out" = "[]" ] && ok "$name" || bad "$name (expected no problems, got $out)"
  else
    jq -e --arg l "$level" --arg w "$want" 'any(.[]; .level == $l and (.msg | contains($w)))' <<<"$out" >/dev/null \
      && ok "$name" || bad "$name (wanted $level containing '$want', got $out)"
  fi
}

check "the shipped example is clean"            "$(cat "$here/examples/.tfpls.yml")" none
check "an approvals block was removed in v0.9.0" $'defaults:\n  approvals: { mode: strict, required: 2 }\nstacks:\n  - name: s\n    workspaces: [a]' error "was removed in v0.9.0"
check "a minimal stack is clean"                $'stacks:\n  - name: s\n    workspaces: [a, b]' none
check "the old workspaces list is one clear error" $'workspaces:\n  - name: dev\n    dir: envs/dev\n    auto_apply: always' error "removed in v0.6.0"
check "all settings with valid values are clean" $'defaults: { auto_apply: never, parallelism: 8, lock_timeout: 30s, plan_lock: true, require_reviewed_plan: true, var_file: x.tfvars, policy_dir: p }\nstacks:\n  - name: s\n    workspaces_glob: "ws/*"' none

check "policy_packs as a list is clean"        $'defaults: { policy_packs: [safety, aws-s3] }\nstacks:\n  - name: s\n    workspaces: [a]' none
check "policy_packs must be a list"             $'defaults: { policy_packs: safety }\nstacks:\n  - name: s\n    workspaces: [a]' error "list of strings"
check "policy_packs rejects odd names"          $'defaults: { policy_packs: ["../x"] }\nstacks:\n  - name: s\n    workspaces: [a]' error "pack names"

check "drift_remediate accepts the three modes"  $'defaults: { drift_remediate: additive_only }\nstacks:\n  - name: s\n    workspaces: [a]' none
check "drift_remediate rejects other values"    $'defaults: { drift_remediate: sometimes }\nstacks:\n  - name: s\n    workspaces: [a]' error "never, additive_only or always"



check "notification settings are accepted"    $'defaults: { notify_secret: SLACK_WEBHOOK_X, notify_format: teams, notify_mention: "@here" }\nstacks:\n  - name: s\n    workspaces: [a]' none
check "notify_secret must be a key, not a URL"  $'defaults: { notify_secret: "https://hooks.slack.com/x" }\nstacks:\n  - name: s\n    workspaces: [a]' error "a key of the secret TFPLS_NOTIFY_WEBHOOKS"
check "notify_format is validated"              $'defaults: { notify_format: pager }\nstacks:\n  - name: s\n    workspaces: [a]' error "slack, teams or generic"
check "drift_fix: false is accepted"                $'defaults: { drift_fix: false }\nstacks:\n  - name: s\n    workspaces: [a]' none
check "drift_fix must be true or false"             $'defaults: { drift_fix: "no" }\nstacks:\n  - name: s\n    workspaces: [a]' error "true or false"
check "notify_template accepts a relative path"       $'defaults: { notify_template: .github/tfpls/slack.jq }\nstacks:\n  - name: s\n    workspaces: [a]' none
check "notify_template refuses an absolute path"    $'defaults: { notify_template: /etc/slack.jq }\nstacks:\n  - name: s\n    workspaces: [a]' error "relative path"
check "notify_template refuses .."                   $'defaults: { notify_template: ../slack.jq }\nstacks:\n  - name: s\n    workspaces: [a]' error "relative path"



check "policy_params maps packs to parameters"  $'defaults: { policy_params: { safety: { max_destroys: 3 } } }\nstacks:\n  - name: s\n    workspaces: [a]' none
check "policy_params must map to objects"        $'defaults: { policy_params: { safety: 3 } }\nstacks:\n  - name: s\n    workspaces: [a]' error "must map a pack name"

check "an empty file is valid: the defaults apply" "" none
check "defaults only: the default stack applies"  $'defaults: {}' none
check "top-level overrides are accepted"         $'overrides:\n  "dev-*": { auto_apply: always }' none
check "top-level overrides are validated"        $'overrides:\n  "dev-*": { auto_apply: sometimes }' error "never, always or additive_only"
check "the old list next to stacks is the same error" $'stacks: []\nworkspaces: []' error "docs/migration.md"
check "stacks is not a list"                    $'stacks: {name: s}' error "stacks: must be a list"
check "stack without a name"                    $'stacks:\n  - workspaces: [a]' error 'needs a `name`'
check "stack name with illegal characters"      $'stacks:\n  - name: "a b"\n    workspaces: [a]' error 'needs a `name`'
check "stack without a workspace source"        $'stacks:\n  - name: s' error 'needs one of `workspaces`'
check "var_file names the expected form"       $'stacks:\n  - name: s\n    workspaces: [a]\n    var_file: 3' error "env/{workspace}.tfvars"
check "two workspace sources"                   $'stacks:\n  - name: s\n    workspaces: [a]\n    workspaces_glob: "x/*"' error "set only one of"
check "duplicate stack names"                   $'stacks:\n  - {name: s, workspaces: [a]}\n  - {name: s, workspaces: [b]}' error "used more than once"
check "workspaces is not a list of strings"     $'stacks:\n  - name: s\n    workspaces: [1, 2]' error "stacks[0].workspaces: must be a list of strings"
check "ignore is a string"                      $'stacks:\n  - name: s\n    workspaces: [a]\n    ignore: "docs/**"' error "stacks[0].ignore: must be a list of strings"
check "triggers value is not a list"            $'stacks:\n  - name: s\n    workspaces: [a]\n    triggers: { a: b }' error "triggers[\"a\"]"
check "overrides is a list"                     $'stacks:\n  - name: s\n    workspaces: [a]\n    overrides: []' error "overrides: must be a map"
check "an override value is not a map"          $'stacks:\n  - name: s\n    workspaces: [a]\n    overrides: { "prod-*": always }' error "must be a map of settings"
check "bad auto_apply"                          $'stacks:\n  - name: s\n    workspaces: [a]\n    auto_apply: sometimes' error "must be never, always or additive_only"
check "bad auto_apply inside an override"       $'stacks:\n  - name: s\n    workspaces: [a]\n    overrides: { "prod-*": { auto_apply: yes } }' error "overrides[\"prod-*\"].auto_apply"
check "plan_lock as a quoted string"            $'stacks:\n  - name: s\n    workspaces: [a]\n    plan_lock: "false"' error "true or false (without quotes)"
check "lock_timeout as a bare number"           $'stacks:\n  - name: s\n    workspaces: [a]\n    lock_timeout: 5' error "30s, 5m or 1h"
check "parallelism zero"                        $'stacks:\n  - name: s\n    workspaces: [a]\n    parallelism: 0' error "whole number of 1 or more"
check "parallelism fractional"                  $'stacks:\n  - name: s\n    workspaces: [a]\n    parallelism: 2.5' error "whole number of 1 or more"
check "var_file is a list"                      $'stacks:\n  - name: s\n    workspaces: [a]\n    var_file: [x]' error "stacks[0].var_file: must be a string"
check "bad value in defaults"                   $'defaults: { plan_lock: "yes" }\nstacks:\n  - name: s\n    workspaces: [a]' error "defaults.plan_lock"
check "create_workspace was removed (any value)" $'defaults: { create_workspace: true }\nstacks:\n  - name: s\n    workspaces: [a]' error "tfpls never creates a tofu workspace"

check "unknown key is a warning"                $'stacks:\n  - name: s\n    workspaces: [a]\n    colour: blue' warning 'unknown key `colour` is ignored'
check "typo autoapply => did you mean"          $'stacks:\n  - name: s\n    workspaces: [a]\n    autoapply: always' warning 'did you mean `auto_apply`?'
check "typo auto-apply (dash) => did you mean"  $'stacks:\n  - name: s\n    workspaces: [a]\n    auto-apply: always' warning 'did you mean `auto_apply`?'
check "typo in an override"                     $'stacks:\n  - name: s\n    workspaces: [a]\n    overrides: { "x": { planlock: false } }' warning 'did you mean `plan_lock`?'
check "unknown top-level key"                   $'stacks:\n  - name: s\n    workspaces: [a]\nstack: []' warning 'config: unknown key `stack`'
check "unknown key in defaults"                 $'defaults: { nope: 1 }\nstacks:\n  - name: s\n    workspaces: [a]' warning 'defaults: unknown key `nope`'

# a typo only warns: the run is not blocked
out=$(printf '%s\n' $'stacks:\n  - name: s\n    workspaces: [a]\n    autoapply: always' | yq -o=json '.' | jq -c -f "$V")
jq -e 'all(.[]; .level == "warning")' <<<"$out" >/dev/null && ok "warnings alone do not stop a run (no error level)" || bad "typo escalated to error"

done_tests
