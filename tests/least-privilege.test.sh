#!/usr/bin/env bash
# Least privilege: every job of the reusable workflows asks for a subset of what the caller grants, checkouts keep no token in
# .git/config, and the commands that run the pull request's code (tofu, conftest) get neither the GitHub token nor the plan key.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
W="$here/.github/workflows"

# the grant the caller written by tools/onboard gives (a job asking for more would stop the whole run at startup)
level() { case "$1" in write) echo 2;; read) echo 1;; *) echo 0;; esac; }
declare_grant() { case "$1" in contents|pull-requests|issues|id-token) echo write;; actions) echo read;; *) echo none;; esac; }
for f in plan apply comment-ops drift drift-fix locks; do
  for job in $(yq -r '.jobs | keys | .[]' "$W/$f.yml"); do
    perms=$(yq -r ".jobs.\"$job\".permissions // \"\" | to_entries | .[] | .key + \" \" + .value" "$W/$f.yml" 2>/dev/null)
    if [ -z "$perms" ]; then bad "$f.yml job $job has no permissions: block"; continue; fi
    over=""
    while read -r k v; do
      [ "$(level "$v")" -le "$(level "$(declare_grant "$k")")" ] || over="$over $k:$v"
    done <<<"$perms"
    [ -z "$over" ] && ok "$f.yml job $job asks for a subset of the caller's grant" || bad "$f.yml job $job asks for more than the caller grants:$over"
  done
done
eq "the jobs that only read have no write" "$(yq -r '.jobs.detect.permissions.contents' "$W/plan.yml")|$(yq -r '.jobs.parse.permissions.contents' "$W/comment-ops.yml")" "read|read"
eq "plan jobs cannot write the data branch" "$(yq -r '.jobs.plan.permissions.contents' "$W/plan.yml")" "read"

# no checkout leaves the token in .git/config
missing=""
for f in "$W"/plan.yml "$W"/apply.yml "$W"/comment-ops.yml "$W"/drift.yml "$W"/drift-fix.yml "$W"/locks.yml "$here"/actions/prepare/action.yml; do
  n=$(grep -c 'uses: actions/checkout@' "$f"); k=$(grep -c 'persist-credentials: false' "$f")
  [ "$n" = "$k" ] || missing="$missing $(basename "$f")($n/$k)"
done
[ -z "$missing" ] && ok "every checkout sets persist-credentials: false" || bad "checkouts that keep the token:$missing"

# tofu and conftest run without the GitHub token and the plan key (unlock.sh's own functions, taken from the real script)
mkdir -p "$T/bin"
for c in tofu conftest; do printf '#!/bin/sh\nenv | grep -E "^(GH_TOKEN|GITHUB_TOKEN|PLAN_ENCRYPTION_KEY|AWS_ACCESS_KEY_ID)=" | sort\n' > "$T/bin/$c"; chmod +x "$T/bin/$c"; done
# shellcheck disable=SC2043  # one script is left; the loop stays for the next one
for script in actions/unlock/unlock.sh; do
  sed -n '/^tofu() /p;/^conftest() /p' "$here/$script" > "$T/fn.sh"
  got=$( . "$T/fn.sh"; PATH="$T/bin:$PATH" GH_TOKEN=secret1 GITHUB_TOKEN=secret2 PLAN_ENCRYPTION_KEY=secret3 AWS_ACCESS_KEY_ID=AKIA tofu version | tr '\n' ' ')
  eq "$script: tofu gets no GitHub token and no plan key (the cloud credentials stay)" "$got" "AWS_ACCESS_KEY_ID=AKIA "
done
# the engine of the tofu action (tfpls tofu) is checked in tests/tofu-run.test.sh: tofu and conftest get neither either

done_tests
