#!/usr/bin/env bash
# Tests for `tfpls dashboard build` with fixture data (no network).
set -uo pipefail
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
build_bin tfpls "$T/tfpls" || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
B="$T/tfpls dashboard build"
mkdir -p "$T/d/drift" "$T/d/audit"
cat > "$T/d/drift/terraform_dev.json" <<'J'
{"name":"terraform/dev","state":"in-sync","since":"2026-10-10T05:00:00Z","checked":"2026-10-10T09:00:00Z","issue":null,"summary":"No changes."}
J
cat > "$T/d/drift/terraform_prod.json" <<'J'
{"name":"terraform/prod","state":"drift","since":"2026-10-09T05:00:00Z","checked":"2026-10-10T09:00:00Z","issue":65,"summary":"+0 ~1 -0"}
J
cat > "$T/d/drift/old_style.json" <<'J'
{"state":"error","since":"2026-10-10T01:00:00Z","checked":"2026-10-10T09:00:00Z","issue":null,"summary":"plan failed"}
J
cat > "$T/d/audit/2026-10.jsonl" <<'J'
{"ts":"2026-10-10T06:00:00Z","kind":"apply","status":"applied","workspace":"terraform/dev","actor":"alice","run":"https://github.com/o/r/actions/runs/1","reviewed":true,"counts":{"add":0,"change":1,"destroy":0}}
{"ts":"2026-10-10T07:00:00Z","kind":"apply","status":"applied","workspace":"terraform/dev","actor":"alice","run":"https://github.com/o/r/actions/runs/2","reviewed":false,"counts":null,"duration_s":40}
{"ts":"2026-10-10T08:00:00Z","kind":"apply","status":"applied","workspace":"terraform/only-audit","actor":"x","run":"javascript:alert(1)"}
not json at all
{"ts":"2026-10-10T08:30:00Z","kind":"lock-cleanup","status":"unlocked","workspace":"terraform/<img src=x onerror=alert(1)>","actor":"","run":"https://github.com/o/r/actions/runs/3"}
J
REPO=o/r TFPLS_NOW=2026-10-10T10:00:00Z $B "$T/d" "$T/out" >"$T/log" 2>&1
data() { sed -n '/<script id="data"/,/<\/script>/p' "$T/out/index.html" | sed '1s/^<script[^>]*>//;$s/<\/script>$//'; }
# The data block is one line of compact JSON with a fixed key order: {"events":[...],"generated":...,"repo":...,"workspaces":[{...}]}.
# It is read with grep and sed here; that it parses as JSON is tested in internal/dashboard (TestDataBlockIsJSON).
events() { sed 's/\],"generated".*$//'; }                                   # the events part of a data block
tss() { events | grep -o '"ts":"[^"]*"' | sed 's/^"ts":"\(.*\)"$/\1/'; }    # the event times, in feed order
ws() { grep -cE "\{\"name\":\"$1\"[^{}]*$2"; }                             # ws <name> <ERE>: 1 when that workspace object matches (it has no nested object but last_apply)

eq "page is written" "$(test -s "$T/out/index.html" && echo yes)" yes
eq "data block parses as JSON" "$(data | grep -c '"generated":"2026-10-10T10:00:00Z","repo":"o/r","workspaces":\[')" 1
eq "workspaces from drift state and audit-only ones" "$(data | sed 's/^.*"workspaces":\[//' | grep -o '"name":"[^"]*"' | sed 's/^"name":"\(.*\)"$/\1/' | LC_ALL=C sort | paste -sd, -)" "old/style,terraform/dev,terraform/only-audit,terraform/prod"
eq "a state file without a name falls back to the file name" "$(data | ws old/style '"state":"error"')" 1
eq "audit-only workspace is shown as unknown" "$(data | ws terraform/only-audit '"state":"unknown"')" 1
eq "last apply is the newest successful apply" "$(data | ws terraform/dev '"last_apply":\{[^{}]*"run":"https://github.com/o/r/actions/runs/2"\}\}')" 1
eq "issue link is built from the repo" "$(data | ws terraform/prod '"issue_url":"https://github.com/o/r/issues/65"')" 1
eq "stack comes from the workspace name" "$(data | ws terraform/prod '"stack":"terraform"')" 1
eq "feed is newest first and skips unreadable lines" "$(data | tss | paste -sd, -)" "2026-10-10T08:30:00Z,2026-10-10T08:00:00Z,2026-10-10T07:00:00Z,2026-10-10T06:00:00Z"
eq "feed length can be limited" "$(EVENTS=2 REPO=o/r TFPLS_NOW=2026-10-10T10:00:00Z $B "$T/d" "$T/o2" >/dev/null 2>&1; sed -n '/<script id="data"/,/<\/script>/p' "$T/o2/index.html" | sed '1s/^<script[^>]*>//;$s/<\/script>$//' | events | grep -o '"ts":' | wc -l | tr -d ' ')" 2

eq "failed or skipped applies are not shown as the last apply" "$(data | ws terraform/prod '"last_apply":null\}')" 1
eq "an in-sync workspace shows no issue link" "$(data | ws terraform/dev '"issue_url":null,')" 1


# the "Last 30 days" block is gone (no heading, no container, no insights in the data)
eq "the embedded data has no insights object" "$(data | grep -c '"insights"')" 0
{ ! grep -q 'Last 30 days\|id="ins"\|insights' "$T/out/index.html"; } && ok "the page has no 'Last 30 days' block" || bad "Last 30 days block still in the page"
grep -q 'Recent activity' "$T/out/index.html" && grep -q 'id="feed"' "$T/out/index.html" && grep -q 'data-k="name"' "$T/out/index.html" && ok "the workspace table and the recent activity feed are still there" || bad "table or feed missing"
mkdir -p "$T/empty/drift" "$T/empty/audit"
REPO=o/r TFPLS_NOW=2026-10-10T10:00:00Z $B "$T/empty" "$T/eo" >/dev/null 2>&1; rc=$?
{ [ $rc -eq 0 ] && grep -q 'id="feed"' "$T/eo/index.html" && [ -s "$T/eo/summary.md" ]; } && ok "a page built from an empty data branch (no audit events) still renders (negative case)" || bad "empty data: rc=$rc"

# safety: data can never break out of the script block, and the page makes no external requests
eq "no raw < inside the data block" "$(data | grep -c '<')" 0
eq "markup in a workspace name survives as text" "$(data | events | grep -cF '{"events":[{"ts":"2026-10-10T08:30:00Z","kind":"lock-cleanup","status":"unlocked","workspace":"terraform/\u003cimg src=x onerror=alert(1)\u003e",')" 1
eq "exactly one script element carries data" "$(grep -c 'id="data"' "$T/out/index.html")" 1
eq "no external script, style or font requests" "$(grep -cE '(src|href)="https?://' "$T/out/index.html")" 0
eq "the page never assigns innerHTML" "$(grep -c 'innerHTML' "$T/out/index.html")" 0
eq "only https links are rendered (javascript: is dropped)" "$(grep -c "test(u)" "$T/out/index.html")" 1

# summary markdown
eq "summary has a status table" "$(grep -c '^| `terraform/prod` | 🟠 drift | +0 ~1 -0' "$T/out/summary.md")" 1
eq "summary counts" "$(sed -n 3p "$T/out/summary.md")" "4 workspaces: 1 in sync, 1 drifted, 1 failing"
eq "empty data branch still builds" "$(mkdir -p "$T/empty"; $B "$T/empty" "$T/o3" >/dev/null 2>&1; echo $?)" 0
eq "empty page says there is no activity" "$(grep -c 'No audit events in the last 24 hours' "$T/o3/index.html")" 1

# the feed shows only the last 24 hours before the build time
mkdir -p "$T/win/drift" "$T/win/audit"
cp "$T/d/drift/terraform_dev.json" "$T/win/drift/"
cat > "$T/win/audit/2026-10.jsonl" <<'J'
{"ts":"2026-10-09T09:59:00Z","kind":"apply","status":"applied","workspace":"terraform/dev","actor":"a","run":"https://github.com/o/r/actions/runs/20"}
{"ts":"2026-10-09T10:01:00Z","kind":"apply","status":"applied","workspace":"terraform/dev","actor":"a","run":"https://github.com/o/r/actions/runs/21"}
{"ts":"2026-10-10T09:00:00Z","kind":"apply","status":"failed","workspace":"terraform/dev","actor":"a","run":"https://github.com/o/r/actions/runs/22"}
{"ts":"yesterday","kind":"apply","status":"applied","workspace":"terraform/dev","actor":"a","run":"https://github.com/o/r/actions/runs/23"}
{"kind":"apply","status":"applied","workspace":"terraform/dev","actor":"a","run":"https://github.com/o/r/actions/runs/24"}
J
feed() { sed -n '/<script id="data"/,/<\/script>/p' "$1/index.html" | sed '1s/^<script[^>]*>//;$s/<\/script>$//'; }   # the data block of a built page
REPO=o/r TFPLS_NOW=2026-10-10T10:00:00Z $B "$T/win" "$T/winout" >/dev/null 2>&1
eq "an event 23 h 59 min old is in the feed, one 24 h 01 min old is not, newest first, bad or missing ts left out" "$(feed "$T/winout" | tss | paste -sd, -)" "2026-10-10T09:00:00Z,2026-10-09T10:01:00Z"
REPO=o/r TFPLS_NOW=2026-10-10T10:00:00Z EVENTS=1 $B "$T/win" "$T/wincap" >/dev/null 2>&1
eq "the EVENTS cap applies on top of the window" "$(feed "$T/wincap" | tss | paste -sd, -)" "2026-10-10T09:00:00Z"
eq "the summary lists only events of the window" "$(grep -c '^- 2026-' "$T/winout/summary.md")" 2

# audit history that is all older than 24 hours: empty feed, build succeeds, last apply still shown
mkdir -p "$T/old/drift" "$T/old/audit"
cp "$T/d/drift/terraform_dev.json" "$T/old/drift/"
cat > "$T/old/audit/2026-10.jsonl" <<'J'
{"ts":"2026-10-01T06:00:00Z","kind":"apply","status":"applied","workspace":"terraform/dev","actor":"a","run":"https://github.com/o/r/actions/runs/30"}
J
REPO=o/r TFPLS_NOW=2026-10-10T10:00:00Z $B "$T/old" "$T/oldout" >/dev/null 2>&1; rc=$?
eq "an audit history older than 24 hours builds (exit code 0)" "$rc" 0
eq "an audit history older than 24 hours gives an empty feed" "$(feed "$T/oldout" | events | grep -c '"ts":')" 0
eq "the empty page text names the 24 hours" "$(grep -c 'No audit events in the last 24 hours' "$T/oldout/index.html")" 1
eq "the empty summary text names the 24 hours" "$(grep -c '^_No audit events in the last 24 hours\._$' "$T/oldout/summary.md")" 1
eq "an apply older than 24 hours is still the last apply" "$(feed "$T/oldout" | ws terraform/dev '"last_apply":\{[^{}]*"run":"https://github.com/o/r/actions/runs/30"\}\}')" 1

# a large audit history must not break the page (a 128 KiB command-line argument cannot be passed to a program)
mkdir -p "$T/big/drift" "$T/big/audit"
cp "$T/d/drift/terraform_dev.json" "$T/big/drift/"
for i in $(seq 1 1200); do printf '{"ts":"2026-10-%02dT06:%02d:00Z","kind":"apply","status":"applied","workspace":"terraform/dev","actor":"someone","run":"https://github.com/o/r/actions/runs/%d","reviewed":true,"counts":{"add":0,"change":1,"destroy":0,"replace":0},"duration_s":40,"via":"auto-apply"}\n' $((i%27+1)) $((i%60)) "$i"; done > "$T/big/audit/2026-10.jsonl"
REPO=o/r TFPLS_NOW=2026-10-27T10:00:00Z EVENTS=5 $B "$T/big" "$T/bigout" >"$T/biglog" 2>&1
eq "a 400 KB audit history still builds the page" "$(feed "$T/bigout" | events | grep -o '"ts":' | wc -l | tr -d ' ')" 5

done_tests
