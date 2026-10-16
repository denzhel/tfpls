#!/usr/bin/env bash
# Write the audit event (AUDIT=true) of a finished run to the data branch.
# Best-effort: never fails the job. Test overrides: TFPLS_REMOTE_URL, TFPLS_NOW, TFPLS_RETRY_SLEEP.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=../record/record.sh
. "$here/../record/record.sh"

[ "${AUDIT:-false}" = true ] && record_prepare || exit 0
BRANCH="${BRANCH:-tfpls-data}"
if tfpls_data_write record_write "audit: $KIND $WORKSPACE $STATUS"; then echo "data branch $BRANCH updated"
else echo "::warning::could not write the audit event to $BRANCH"; fi
exit 0
