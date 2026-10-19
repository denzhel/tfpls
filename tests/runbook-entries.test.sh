#!/usr/bin/env bash
# The troubleshooting and background sections that the simulation findings asked for exist and point at each other.
# Run: bash tests/runbook-entries.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
has() { grep -qF -- "$2" "$here/$1"; }
check() { if has "$2" "$3"; then ok "$1"; else bad "$1 ($2 lacks: $3)"; fi; }

check "credentials explains that the plan role needs more than Describe*" docs/credentials.md "### Resource read is more than \`Describe*\`"
check "credentials names the example action" docs/credentials.md "ec2:GetManagedPrefixListEntries"
check "the runbook has the 403-on-refresh entry" docs/runbooks.md "## Every plan of a workspace fails with a 403 on refresh"
check "the runbook links to the credentials section" docs/runbooks.md "credentials.md#resource-read-is-more-than-describe"
check "credentials links to the runbook entry" docs/credentials.md "runbooks.md#every-plan-of-a-workspace-fails-with-a-403-on-refresh"

check "reviewed-plans explains what a plan contains" docs/reviewed-plans.md "## What a plan contains"
check "the runbook has the entry for a plan bigger than the diff" docs/runbooks.md "## The plan shows resources my pull request did not change"
check "the runbook links to the reviewed-plans section" docs/runbooks.md "reviewed-plans.md#what-a-plan-contains"
check "reviewed-plans links to the runbook entry" docs/reviewed-plans.md "runbooks.md#the-plan-shows-resources-my-pull-request-did-not-change"
check "the auto_apply reference links to it" docs/reference.md "reviewed-plans.md#what-a-plan-contains"

check "the runbook explains the apply-before-merge window" docs/runbooks.md "## Apply before merge: the window"
check "reviewed-plans links to the apply-before-merge window" docs/reviewed-plans.md "runbooks.md#apply-before-merge-the-window"
check "the runbook gives the safe order" docs/runbooks.md "Apply, then merge right away"

done_tests
