#!/usr/bin/env bash
# summarize.sh <plan.json|-> [json|header]   (default: json); "-" reads the summary JSON (the `json` mode output) from stdin
# Turns `tofu show -json` output into counts and the changed addresses (see summarize.jq).
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
plan=${1:?usage: summarize.sh <plan.json|-> [json|header]}; mode=${2:-json}
if [ "$plan" = - ]; then json=$(cat); else json=$(jq -c -f "$here/summarize.jq" "$plan") || exit 1; fi
case "$mode" in
  json)   printf '%s\n' "$json";;
  header) jq -r .header <<<"$json";;
  *) echo "unknown mode: $mode" >&2; exit 2;;
esac
