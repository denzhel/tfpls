#!/usr/bin/env bash
# Tests for `tfpls tofu rollback` (hints after a failed apply)
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# ---- rollback hints
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
(cd "$here" && go build -o "$T/tfpls" ./cmd/tfpls) || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
cat > "$T/apply.txt" <<'A'
aws_sqs_queue.this["a"]: Creating...
aws_sqs_queue.this["a"]: Creation complete after 1s [id=x]
aws_s3_bucket.this["b"]: Modifications complete after 2s [id=y]
╷
│ Error: creating S3 Bucket (dup): BucketAlreadyExists
│ 
│   with aws_s3_bucket.this["c"],
╵
A
out=$("$T/tfpls" tofu rollback "$T/apply.txt" 0123456789abcdef)
eq "lists what was already changed" "$(grep -c 'aws_sqs_queue.this\["a"\]` (Creation)' <<<"$out")|$(grep -c 'aws_s3_bucket.this\["b"\]` (Modification)' <<<"$out")" "1|1"
eq "quotes the error" "$(grep -c 'BucketAlreadyExists' <<<"$out")" 1
eq "names the commit to revert" "$(grep -c 'git revert 0123456789ab' <<<"$out")" 1
eq "warns against force-unlock" "$(grep -c 'force-unlock' <<<"$out")" 1
printf 'x: Creating...\n│ Error: nope\n' > "$T/none.txt"
eq "a failure before any change says so" "$("$T/tfpls" tofu rollback "$T/none.txt" | grep -c 'No resource was changed')" 1

done_tests
