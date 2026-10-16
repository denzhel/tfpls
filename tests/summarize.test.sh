#!/usr/bin/env bash
# Plan summary (actions/tofu/summarize.jq + render.jq) on fixture plans. Run: bash tests/summarize.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
S="$here/actions/tofu/summarize.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT

plan() { printf '%s' "$1" > "$T/p.json"; }
js()   { bash "$S" "$T/p.json" json; }
hdr()  { bash "$S" "$T/p.json" header; }
ban()  { bash "$S" "$T/p.json" banner; }
rc() { jq -cn --arg a "$1" --argjson acts "$2" '{address:$a, change:{actions:$acts, before:{"secret":"hunter2"}, after:{"secret":"hunter3"}}}'; }
wrap() { jq -cn --argjson r "[$1]" '{resource_changes:$r}'; }

plan "$(wrap "$(rc a '["create"]'),$(rc b '["update"]'),$(rc c '["delete"]'),$(rc d '["delete","create"]'),$(rc e '["no-op"]'),$(rc f '["read"]')")"
eq "header counts create/update/delete/replace and ignores no-op and read" "$(hdr)" "+2 ~1 -2 (1 replace)"
eq "counts as JSON" "$(js | jq -c .counts)" '{"add":2,"change":1,"destroy":2,"replace":1}'
eq "changed addresses carry the action, nothing else" "$(js | jq -c '[.resources[] | .address + ":" + .action]')" '["a:create","b:update","c:delete","d:replace"]'
case "$(js)" in *hunter*) bad "a value leaked into the summary";; *) ok "no attribute value is ever in the summary";; esac
eq "the warning names what is destroyed or replaced" "$(ban)" '> ⚠️ **2 resource(s) will be destroyed or replaced:** `c`, `d` (replace)'
plan "$(wrap "$(rc a '["create"]'),$(rc b '["update"]')")"
eq "no destroy, no warning" "$(ban)" ""
plan "$(wrap "$(for i in $(seq 1 12); do rc "r$i" '["delete"]'; echo ,; done | tr -d '\n' | sed 's/,$//')")"
eq "the warning lists ten and counts the rest" "$(ban | grep -o '… 2 more')" "… 2 more"
plan '{"resource_changes":[]}'; eq "empty plan header" "$(hdr)" "+0 ~0 -0"
plan '{}'; eq "a plan without resource_changes does not break" "$(hdr)" "+0 ~0 -0"
bash "$S" - md </dev/null >/dev/null 2>&1; eq "the removed md mode is an error" "$?" 2

done_tests
