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

done_tests
