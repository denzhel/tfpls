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

# tofu and conftest (the pull request's code) get neither the GitHub token nor the plan key: tests/tofu-run.test.sh (tfpls tofu)
# and internal/unlock (tfpls unlock)

done_tests
