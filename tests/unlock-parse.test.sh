#!/usr/bin/env bash
# Tests for actions/unlock/parse.sh. Run: bash tests/unlock-parse.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
. "$here/../actions/unlock/parse.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# the shape tofu/terraform prints when the state is locked (S3 backend)
LOCKED='
Error: Error acquiring the state lock

Error message: operation error S3: PutObject, https response error StatusCode: 412
Lock Info:
  ID:        138e6af8-18fc-5526-e4fa-40efcf6236f5
  Path:      example-state-bucket/stg-us-west-2/workspace.tf.state
  Operation: OperationTypeApply
  Who:       ABCDE@ip-192-168-75-4.ec2.internal
  Version:   1.10.6
  Created:   2021-10-31 09:27:42.742425 +0000 UTC
  Info:

OpenTofu acquires a state lock to protect the state from being written
by multiple users at the same time.'

parse_lock "$LOCKED"; rc=$?
eq "returns 0 for a lock block" "$rc" "0"
eq "lock ID" "$LOCK_ID" "138e6af8-18fc-5526-e4fa-40efcf6236f5"
eq "holder" "$LOCK_WHO" "ABCDE@ip-192-168-75-4.ec2.internal"
eq "operation" "$LOCK_OP" "OperationTypeApply"
eq "created" "$LOCK_CREATED" "2021-10-31 09:27:42.742425 +0000 UTC"

parse_lock "No changes. Your infrastructure matches the configuration."; rc=$?
eq "no lock error => returns 1" "$rc" "1"
parse_lock "Error: Error acquiring the state lock
Lock Info:
  Who: someone"; rc=$?
eq "lock error without an ID => returns 2" "$rc" "2"
parse_lock "Error: Error acquiring the state lock
Lock Info:
  ID: x; rm -rf /"; rc=$?
eq "unsafe ID text is rejected" "$rc" "2"

NOW=$(date -u -j -f '%Y-%m-%d %H:%M:%S' '2026-10-09 12:00:00' +%s 2>/dev/null || date -u -d '2026-10-09 12:00:00' +%s)
eq "age: 90 minutes" "$(lock_age_minutes '2026-10-09 10:30:00.1 +0000 UTC' "$NOW")" "90"
eq "age: 3 minutes" "$(lock_age_minutes '2026-10-09 11:57:00 +0000 UTC' "$NOW")" "3"
eq "age: unparsable => empty" "$(lock_age_minutes 'yesterday-ish' "$NOW")" ""

done_tests
