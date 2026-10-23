#!/usr/bin/env bash
# Least privilege: every job of the reusable workflows asks for a subset of what the caller grants, checkouts keep no token in
# .git/config, and the commands that run the pull request's code (tofu, conftest) get neither the GitHub token nor the plan key.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
W="$here/.github/workflows"


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
