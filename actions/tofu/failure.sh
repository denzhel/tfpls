#!/usr/bin/env bash
# failure.sh <output.txt> <workspace-label>  -> Markdown: the first errors of a failed init/plan and, for the common causes, what to do
set -uo pipefail
f=${1:?usage: failure.sh <output.txt> <label>}; label=${2:-<workspace>}
clean=$(sed -E 's/\x1b\[[0-9;]*m//g; s/^[│╷╵ ]+//; s/[│╷╵]//g' "$f")
errs=$(grep -E '^ *Error:' <<<"$clean" | sed -E 's/^ *//' | awk '!seen[$0]++' | head -3)
[ -n "$errs" ] || exit 0
printf '%s\n' "$errs" | sed 's/^/- /' | cut -c1-300
if grep -q 'Error acquiring the state lock' <<<"$clean"; then
  id=$(sed -n 's/^ *ID: *//p' <<<"$clean" | head -1 | tr -d '[:space:]'); who=$(sed -n 's/^ *Who: *//p' <<<"$clean" | head -1)
  printf '\n🔒 The state is locked%s%s. If no run is working on `%s`, release it with `/tofu unlock %s`; otherwise wait for that run.\n' "${id:+ (lock \`$id\`}" "${id:+${who:+, held by \`$who\`)}}" "$label" "$label"
elif grep -qE 'No valid credential sources|InvalidClientTokenId|ExpiredToken|AccessDenied|SignatureDoesNotMatch|dial tcp|connection refused|no such host' <<<"$clean"; then
  printf '\n🔑 Looks like a credentials or connection problem: check the `TFPLS_ROLE_ARN` variable (or the `TFPLS_ENV_JSON` secret) and the backend endpoint ([runbook](https://github.com/%s/blob/main/docs/runbooks.md)).\n' "${GITHUB_ACTION_REPOSITORY:-denzhel/tfpls}"
elif grep -q 'Backend configuration changed\|Backend initialization required' <<<"$clean"; then
  printf '\n🧱 The backend configuration changed: run `tofu init -reconfigure` locally against the new backend (or `-migrate-state` to move the state) before this pipeline can use it.\n'
fi
