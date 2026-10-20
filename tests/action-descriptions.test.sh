#!/usr/bin/env bash
# GitHub evaluates `${{ ... }}` inside an action's input descriptions as an expression, and an expression that is not valid
# there (for example `needs`) stops the action from loading at all ("Unrecognized named-value"). Run: bash tests/action-descriptions.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
. "$here/lib.sh"
n=0
for f in "$here"/../actions/*/action.yml; do
  n=$((n + 1))
  hits=$(yq -r '(.inputs // {}) | to_entries[] | select((.value.description // "") | contains("${{")) | .key' "$f")
  [ -z "$hits" ] && ok "$(basename "$(dirname "$f")"): no expression in an input description" || bad "$f: input description with an expression: $hits"
done
[ "$n" -gt 10 ] && ok "checked $n actions" || bad "found only $n actions"
done_tests
