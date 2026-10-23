#!/usr/bin/env bash
# The cleanup functions of run.sh and unlock.sh (extracted from the real files) must remove everything that can hold
# secret values from the workspace and nothing that belongs to the repository. Run: bash tests/cleanup.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT

mkws() { rm -rf "$T/repo"; mkdir -p "$T/repo/stack/.terraform/providers" "$T/repo/other"
  for f in tfplan plan.json plan.txt apply.txt init.txt policy.txt unlock.txt; do echo secret > "$T/repo/stack/$f"; done
  echo x > "$T/repo/stack/.terraform/providers/p"; echo repo > "$T/repo/stack/main.tf"; echo repo > "$T/repo/stack/.terraform.lock.hcl"
  echo keep > "$T/repo/other/plan.json"; }
remaining() { (cd "$T/repo/stack" && ls -A | tr '\n' ' '); }

# shellcheck disable=SC2043  # one script left (tofu); unlock is Go now
for script in actions/tofu/run.sh; do
  fn=$(sed -n '/^cleanup() {/,/^}/p' "$here/$script")
  [ -n "$fn" ] && ok "$script defines cleanup()" || { bad "$script has no cleanup()"; continue; }
  grep -q '^trap cleanup EXIT' "$here/$script" && ok "$script runs it on EXIT" || bad "$script: no EXIT trap"
  mkws; ( eval "$fn"; REPO_ROOT="$T/repo" DIR=stack cleanup )
  left=$(remaining)
  case "$script" in
    *run.sh) want=".terraform.lock.hcl main.tf unlock.txt ";;   # run.sh does not own unlock.txt
    *)       want=".terraform.lock.hcl apply.txt main.tf plan.json plan.txt policy.txt tfplan ";;
  esac
  [ "$left" = "$want" ] && ok "$script removes its working files and keeps the repo files" || bad "$script left: '$left' (want '$want')"
  [ -f "$T/repo/other/plan.json" ] && ok "$script does not touch other directories" || bad "$script touched another directory"
  ( eval "$fn"; REPO_ROOT="$T/repo" DIR=does-not-exist cleanup ) && ok "$script tolerates a missing directory" || bad "$script failed on a missing directory"
  ( eval "$fn"; REPO_ROOT="" DIR="" cleanup ) && ok "$script tolerates empty variables (never deletes in the current directory)" || bad "$script empty vars"
done

# the composite actions also clean up when the script was killed (always-run last step)
for a in tofu unlock; do  # (the cleanup of `tfpls unlock` itself is tested in internal/unlock)
  [ "$(yq -r '.runs.steps[-1].if' "$here/actions/$a/action.yml")" = "always()" ] && ok "actions/$a ends with an always() cleanup step" || bad "actions/$a: last step is not always()"
done

# the always-run step of the tofu action cleans the directory of the ENTRY (callers pass only `entry`, never `dir`)
step_env=$(yq -r '.runs.steps[-1].env.DIR' "$here/actions/tofu/action.yml")
case "$step_env" in *"fromJSON(inputs.entry"*".dir"*) ok "the cleanup step falls back to the directory of the entry";; *) bad "cleanup DIR does not use the entry: $step_env";; esac
mkdir -p "$T/repo2/envs/dev"; touch "$T/repo2/envs/dev/tfplan" "$T/repo2/envs/dev/show.txt" "$T/repo2/envs/dev/plan.json" "$T/repo2/envs/dev/main.tf"; mkdir -p "$T/repo2/envs/dev/.terraform"
yq -r '.runs.steps[-1].run' "$here/actions/tofu/action.yml" > "$T/step.sh"
REPO_ROOT="$T/repo2" DIR=envs/dev bash "$T/step.sh"
eq "the step removes the plan files (including show.txt) and .terraform, and keeps the code" "$(ls -A "$T/repo2/envs/dev" | tr '\n' ' ')" "main.tf "
( REPO_ROOT="$T/repo2" DIR="" bash "$T/step.sh" ) && ok "an empty directory is tolerated (nothing is deleted)" || bad "empty DIR"

done_tests
