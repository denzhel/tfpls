#!/usr/bin/env bash
# Plan summary (`tfpls tofu summarize`) on fixture plans. Run: bash tests/summarize.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
(cd "$here" && go build -o "$T/tfpls" ./cmd/tfpls) || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
S="$T/tfpls tofu summarize"

plan() { printf '%s' "$1" > "$T/p.json"; }
js()   { $S "$T/p.json" json; }
hdr()  { $S "$T/p.json" header; }
rc() { jq -cn --arg a "$1" --argjson acts "$2" '{address:$a, change:{actions:$acts, before:{"secret":"hunter2"}, after:{"secret":"hunter3"}}}'; }
wrap() { jq -cn --argjson r "[$1]" '{resource_changes:$r}'; }

plan "$(wrap "$(rc a '["create"]'),$(rc b '["update"]'),$(rc c '["delete"]'),$(rc d '["delete","create"]'),$(rc e '["no-op"]'),$(rc f '["read"]')")"
eq "header counts create/update/delete/replace and ignores no-op and read" "$(hdr)" "+2 ~1 -2 (1 replace)"
eq "counts as JSON" "$(js | jq -c .counts)" '{"add":2,"change":1,"destroy":2,"replace":1}'
eq "changed addresses carry the action, nothing else" "$(js | jq -c '[.resources[] | .address + ":" + .action]')" '["a:create","b:update","c:delete","d:replace"]'
case "$(js)" in *hunter*) bad "a value leaked into the summary";; *) ok "no attribute value is ever in the summary";; esac
plan '{"resource_changes":[]}'; eq "empty plan header" "$(hdr)" "+0 ~0 -0"
plan '{}'; eq "a plan without resource_changes does not break" "$(hdr)" "+0 ~0 -0"
$S - banner </dev/null >/dev/null 2>&1; eq "the removed banner mode is an error" "$?" 2
$S - md </dev/null >/dev/null 2>&1; eq "the removed md mode is an error" "$?" 2
# the destroy warning line is gone from the comments, the code and the docs (destroys stay in the Plan: line)
eq "no destroy warning line is built or documented anywhere" "$(grep -rln 'destroyed or replaced' "$here/actions" "$here/docs" "$here/README.md" "$here/examples" | wc -l | tr -d ' ')" 0
eq "no comment code reads a destroy banner" "$(cat "$here"/internal/tofu/*.go | grep -c destroy_banner)" 0

done_tests
