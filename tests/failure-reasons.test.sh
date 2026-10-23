#!/usr/bin/env bash
# Tests for `tfpls tofu failure` (what a failed init/plan comment says)
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
(cd "$here" && go build -o "$T/tfpls" ./cmd/tfpls) || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
F="$T/tfpls tofu failure"
has() { grep -qF -- "$2" <<<"$1" && echo yes || echo no; }

cat > "$T/lock.txt" <<'X'
╷
│ Error: Error acquiring the state lock
│ 
│ Error message: operation error S3: PutObject, https response error
│ 
│ Lock Info:
│   ID:        55555555-aaaa-bbbb-cccc-000000000005
│   Path:      tfpls-state/fresh/dev/fresh.tfstate
│   Operation: OperationTypeApply
│   Who:       crashed@runner
│   Version:   1.10.6
╵
X
out=$($F "$T/lock.txt" dev)
eq "the first error is quoted" "$(has "$out" '- Error: Error acquiring the state lock')" yes
eq "a state lock names the lock, the holder and /tofu unlock" "$(has "$out" 'lock `55555555-aaaa-bbbb-cccc-000000000005`, held by `crashed@runner`')|$(has "$out" '`/tofu unlock dev`')" "yes|yes"

printf '\x1b[31mError:\x1b[0m failed to get shared config profile\nNo valid credential sources found\n' > "$T/cred.txt"
out=$($F "$T/cred.txt" dev); eq "colour codes are stripped and a credentials hint is given" "$(has "$out" '- Error: failed to get shared config profile')|$(has "$out" 'credentials or connection problem')" "yes|yes"
printf '│ Error: Failed to query available provider packages\n│ dial tcp 127.0.0.1:1: connect: connection refused\n' > "$T/net.txt"
out=$($F "$T/net.txt" dev); eq "a refused connection also gets the credentials/connection hint" "$(has "$out" 'credentials or connection problem')" yes
printf '│ Error: Backend initialization required, please run "tofu init"\n' > "$T/be.txt"
out=$($F "$T/be.txt" dev); eq "a changed backend explains init -reconfigure" "$(has "$out" 'tofu init -reconfigure')" yes
printf 'Error: Invalid index\nError: Invalid index\nError: Unsupported argument\nError: A fourth one\n' > "$T/many.txt"
out=$($F "$T/many.txt" dev); eq "duplicate errors are shown once and at most three" "$(printf '%s\n' "$out" | grep -c '^- Error')" 3
eq "no error line: nothing to add" "$(printf 'something odd\n' > "$T/none.txt"; $F "$T/none.txt" dev | wc -c | tr -d ' ')" 0

done_tests
