#!/usr/bin/env bash
# Tests for `tfpls finish webhook`: a workspace's notify_secret is a key of the secret TFPLS_NOTIFY_WEBHOOKS (the default,
# SLACK_WEBHOOK, is the secret of that name), the chosen URL is masked before any step can show it, and a wrong key, map or
# URL logs a warning and never fails the job. Plus the wiring in actions/finish/action.yml.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
build_bin tfpls "$T/tfpls" || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
# pick [VAR=value ...]: run the script in an empty environment; prints rc=<status>; the log is in $T/log, the step output in $T/out
pick() { : > "$T/out"; env -i PATH="$PATH" GITHUB_OUTPUT="$T/out" "$@" "$T/tfpls" finish webhook > "$T/log" 2>&1; echo "rc=$?"; }
url() { sed -n 's/^url=//p' "$T/out"; }
warns() { grep -c '^::warning::' "$T/log"; }
# no line of the log but the mask command shows a URL of the test values (all on the host hook.invalid)
shown() { grep -v '^::add-mask::' "$T/log" | grep -c 'hook[.]invalid'; }

D=https://hook.invalid/default
M='{"ONCALL": "https://hook.invalid/oncall", "TEAMS_WEBHOOK": "https://hook.invalid/teams", "SLACK_WEBHOOK": "https://hook.invalid/in-map"}'

# ---- the default
eq "no notify_secret: the secret SLACK_WEBHOOK" "$(pick DEFAULT_HOOK="$D" HOOKS="$M")|$(url)|$(warns)" "rc=0|$D|0"
eq "notify_secret SLACK_WEBHOOK is the same default" "$(pick KEY=SLACK_WEBHOOK DEFAULT_HOOK="$D" HOOKS="$M")|$(url)" "rc=0|$D"
eq "the secret SLACK_WEBHOOK not set: the key SLACK_WEBHOOK of the map" "$(pick HOOKS="$M")|$(url)" "rc=0|https://hook.invalid/in-map"
eq "no webhook at all: nothing is sent, no warning" "$(pick)|$(url)|$(warns)|$(grep -c 'SLACK_WEBHOOK is not set' "$T/log")" "rc=0||0|1"
eq "a map without the default key: the default sends nothing, silently" "$(pick HOOKS='{"ONCALL": "https://hook.invalid/x"}')|$(url)|$(warns)" "rc=0||0"
# ---- a key of TFPLS_NOTIFY_WEBHOOKS
eq "notify_secret ONCALL: its URL from the map, not the default" "$(pick KEY=ONCALL DEFAULT_HOOK="$D" HOOKS="$M")|$(url)|$(warns)" "rc=0|https://hook.invalid/oncall|0"
eq "the chosen URL is masked" "$(grep -cx '::add-mask::https://hook.invalid/oncall' "$T/log")" 1
eq "and shown nowhere else; the log names the key" "$(shown)|$(grep -c 'key `ONCALL` of the secret TFPLS_NOTIFY_WEBHOOKS' "$T/log")" "0|1"
eq "the mask comes before anything else is logged" "$(head -n1 "$T/log")" "::add-mask::https://hook.invalid/oncall"
eq "the default is masked too" "$(pick DEFAULT_HOOK="$D")|$(grep -cx "::add-mask::$D" "$T/log")" "rc=0|1"
eq "a % in the URL is escaped for the mask command" "$(pick KEY=ONCALL HOOKS='{"ONCALL": "https://hook.invalid/a%25b"}')|$(url)|$(grep -cx '::add-mask::https://hook.invalid/a%2525b' "$T/log")" "rc=0|https://hook.invalid/a%25b|1"
eq "a map written over several lines works" "$(pick KEY=TEAMS_WEBHOOK HOOKS="$(printf '{\n  "ONCALL": "https://hook.invalid/oncall",\n  "TEAMS_WEBHOOK": "https://hook.invalid/teams",\n  "SLACK_WEBHOOK": "https://hook.invalid/in-map"\n}')")|$(url)" "rc=0|https://hook.invalid/teams"
eq "spaces around a URL are dropped" "$(pick KEY=ONCALL HOOKS='{"ONCALL": " https://hook.invalid/sp \n"}')|$(url)" "rc=0|https://hook.invalid/sp"
eq "exactly one output line" "$(grep -c . "$T/out")" 1
# ---- negative cases: a warning, nothing sent, the job goes on, and no URL in the log
eq "an unknown key: warning, nothing sent" "$(pick KEY=PAGER DEFAULT_HOOK="$D" HOOKS="$M")|$(url)|$(grep -c '^::warning::notify_secret `PAGER` is not a key of the secret TFPLS_NOTIFY_WEBHOOKS' "$T/log")|$(shown)" "rc=0||1|0"
eq "a key while TFPLS_NOTIFY_WEBHOOKS is not set: says so" "$(pick KEY=ONCALL DEFAULT_HOOK="$D")|$(url)|$(grep -c '^::warning::notify_secret `ONCALL`.*TFPLS_NOTIFY_WEBHOOKS, which is not set' "$T/log")" "rc=0||1"
eq "a map that is not JSON: warning, the value is not shown" "$(pick KEY=ONCALL HOOKS='https://hook.invalid/oops')|$(url)|$(grep -c '^::warning::the secret TFPLS_NOTIFY_WEBHOOKS is not a JSON object' "$T/log")|$(shown)" "rc=0||1|0"
eq "a map that is a list: warning" "$(pick KEY=ONCALL HOOKS='["https://hook.invalid/x"]')|$(url)|$(warns)|$(shown)" "rc=0||1|0"
eq "a broken map with the default key and no SLACK_WEBHOOK: warning" "$(pick HOOKS='{"SLACK_WEBHOOK": ')|$(url)|$(warns)" "rc=0||1"
eq "a value that is a secret name, not a URL: warning" "$(pick KEY=ONCALL HOOKS='{"ONCALL": "SLACK_WEBHOOK_ONCALL"}')|$(url)|$(grep -c '^::warning::the value of `ONCALL` in the secret TFPLS_NOTIFY_WEBHOOKS is not an http(s) URL' "$T/log")" "rc=0||1"
eq "a value that is not a string: warning" "$(pick KEY=ONCALL HOOKS='{"ONCALL": {"url": "https://hook.invalid/x"}}')|$(url)|$(warns)|$(shown)" "rc=0||1|0"
eq "a URL with a line break inside cannot add outputs" "$(pick KEY=ONCALL HOOKS='{"ONCALL": "https://hook.invalid/a\nx=1"}')|$(url)|$(warns)|$(grep -c . "$T/out")" "rc=0||1|1"
eq "a default that is not a URL: warning" "$(pick DEFAULT_HOOK='not a url')|$(url)|$(grep -c '^::warning::the secret SLACK_WEBHOOK is not an http(s) URL' "$T/log")" "rc=0||1"
eq "a key with odd characters is not echoed raw into a workflow command" "$(pick KEY=$'X\n::error::y' HOOKS="$M")|$(grep -c '^::error::' "$T/log")|$(warns)" "rc=0|0|1"


done_tests
