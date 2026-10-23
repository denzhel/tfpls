#!/usr/bin/env bash
# tfpls finish store: the audit event lands on the data branch.
set -uo pipefail
export TFPLS_RETRY_SLEEP=0
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
build_bin tfpls "$T/tfpls" || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
git init -q --bare "$T/remote.git"
G=(git --git-dir="$T/remote.git")
run() { env -i PATH="$PATH" TFPLS_RETRY_SLEEP=0 TFPLS_REMOTE_URL="$T/remote.git" TFPLS_NOW=2026-10-10T10:00:00Z WORKSPACE=terraform/dev NAME=terraform/dev LABEL=dev KIND=apply STATUS=applied REPO=o/r "$@" "$T/tfpls" finish store >"$T/out" 2>&1; echo $?; }
commits() { "${G[@]}" rev-list --count tfpls-data 2>/dev/null || echo 0; }

eq "audit: exit 0" "$(run AUDIT=true)" 0
eq "one commit" "$(commits)" 1
eq "the audit event is there" "$("${G[@]}" show tfpls-data:audit/2026-10.jsonl | grep -c '"workspace":"terraform/dev","kind":"[a-z]*","status":"applied"')" 1
eq "a second run appends: one more commit" "$(run AUDIT=true STATUS=error TFPLS_NOW=2026-10-10T11:00:00Z)|$(commits)|$("${G[@]}" show tfpls-data:audit/2026-10.jsonl | wc -l | tr -d ' ')" "0|2|2"
eq "audit off does nothing" "$(run AUDIT=false)|$(commits)" "0|2"
eq "main is refused (and nothing breaks)" "$(run AUDIT=true BRANCH=main)|$(grep -c refusing "$T/out")" "0|1"
eq "an unreachable remote never fails the job" "$(run AUDIT=true TFPLS_REMOTE_URL="$T/missing.git")" 0

# a remote that rejects every push: ten attempts, then a warning, never a failed job
git init -q --bare "$T/reject.git"; mkdir -p "$T/reject.git/hooks"
printf '#!/bin/sh\necho x >> "%s/attempts"\nexit 1\n' "$T" > "$T/reject.git/hooks/pre-receive"; chmod +x "$T/reject.git/hooks/pre-receive"
: > "$T/attempts"
eq "a remote that always rejects: exit 0" "$(run AUDIT=true TFPLS_REMOTE_URL="$T/reject.git")" 0
eq "and exactly ten pushes were tried" "$(wc -l < "$T/attempts" | tr -d ' ')" 10
grep -q "could not write" "$T/out" && ok "with a warning" || bad "no warning: $(cat "$T/out")"

done_tests
