#!/usr/bin/env bash
# Tests for actions/dashboard/build.sh with fixture data (no network).
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
B="$here/actions/dashboard/build.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
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
REPO=o/r TFPLS_NOW=2026-10-10T10:00:00Z bash "$B" "$T/d" "$T/out" >"$T/log" 2>&1
data() { sed -n '/<script id="data"/,/<\/script>/p' "$T/out/index.html" | sed '1s/^<script[^>]*>//;$s/<\/script>$//'; }

eq "page is written" "$(test -s "$T/out/index.html" && echo yes)" yes
eq "data block parses as JSON" "$(data | jq -r .repo)" "o/r"
eq "workspaces from drift state and audit-only ones" "$(data | jq -r '[.workspaces[].name]|sort|join(",")')" "old/style,terraform/dev,terraform/only-audit,terraform/prod"
eq "a state file without a name falls back to the file name" "$(data | jq -r '.workspaces[]|select(.name=="old/style")|.state')" "error"
eq "audit-only workspace is shown as unknown" "$(data | jq -r '.workspaces[]|select(.name=="terraform/only-audit")|.state')" "unknown"
eq "last apply is the newest successful apply" "$(data | jq -r '.workspaces[]|select(.name=="terraform/dev")|.last_apply.run')" "https://github.com/o/r/actions/runs/2"
eq "issue link is built from the repo" "$(data | jq -r '.workspaces[]|select(.name=="terraform/prod")|.issue_url')" "https://github.com/o/r/issues/65"
eq "stack comes from the workspace name" "$(data | jq -r '.workspaces[]|select(.name=="terraform/prod")|.stack')" "terraform"
eq "feed is newest first and skips unreadable lines" "$(data | jq -r '[.events[].ts]|join(",")')" "2026-10-10T08:30:00Z,2026-10-10T08:00:00Z,2026-10-10T07:00:00Z,2026-10-10T06:00:00Z"
eq "feed length can be limited" "$(EVENTS=2 REPO=o/r bash "$B" "$T/d" "$T/o2" >/dev/null 2>&1; sed -n '/<script id="data"/,/<\/script>/p' "$T/o2/index.html" | sed '1s/^<script[^>]*>//;$s/<\/script>$//' | jq '.events|length')" 2

eq "failed or skipped applies are not shown as the last apply" "$(data | jq -r '.workspaces[]|select(.name=="terraform/prod")|.last_apply')" "null"
eq "an in-sync workspace shows no issue link" "$(data | jq -r '.workspaces[]|select(.name=="terraform/dev")|.issue_url')" "null"


# the "Last 30 days" block is gone (no heading, no container, no insights in the data)
eq "the embedded data has no insights object" "$(data | jq -r 'has("insights")')" "false"
{ ! grep -q 'Last 30 days\|id="ins"\|insights' "$T/out/index.html"; } && ok "the page has no 'Last 30 days' block" || bad "Last 30 days block still in the page"
grep -q 'Recent activity' "$T/out/index.html" && grep -q 'id="feed"' "$T/out/index.html" && grep -q 'data-k="name"' "$T/out/index.html" && ok "the workspace table and the recent activity feed are still there" || bad "table or feed missing"
mkdir -p "$T/empty/drift" "$T/empty/audit"
REPO=o/r TFPLS_NOW=2026-10-10T10:00:00Z bash "$B" "$T/empty" "$T/eo" >/dev/null 2>&1; rc=$?
{ [ $rc -eq 0 ] && grep -q 'id="feed"' "$T/eo/index.html" && [ -s "$T/eo/summary.md" ]; } && ok "a page built from an empty data branch (no audit events) still renders (negative case)" || bad "empty data: rc=$rc"

# safety: data can never break out of the script block, and the page makes no external requests
eq "no raw < inside the data block" "$(data | grep -c '<')" 0
eq "markup in a workspace name survives as text" "$(data | jq -r '.events[0].workspace')" 'terraform/<img src=x onerror=alert(1)>'
eq "exactly one script element carries data" "$(grep -c 'id="data"' "$T/out/index.html")" 1
eq "no external script, style or font requests" "$(grep -cE '(src|href)="https?://' "$T/out/index.html")" 0
eq "the page never assigns innerHTML" "$(grep -c 'innerHTML' "$T/out/index.html")" 0
eq "only https links are rendered (javascript: is dropped)" "$(grep -c "test(u)" "$T/out/index.html")" 1

# summary markdown
eq "summary has a status table" "$(grep -c '^| `terraform/prod` | 🟠 drift | +0 ~1 -0' "$T/out/summary.md")" 1
eq "summary counts" "$(sed -n 3p "$T/out/summary.md")" "4 workspaces: 1 in sync, 1 drifted, 1 failing"
eq "empty data branch still builds" "$(mkdir -p "$T/empty"; bash "$B" "$T/empty" "$T/o3" >/dev/null 2>&1; echo $?)" 0
eq "empty page says there is no activity" "$(grep -c 'No audit events yet' "$T/o3/index.html")" 1

# a large audit history must not break the page (a 128 KiB command-line argument cannot be passed to jq)
mkdir -p "$T/big/drift" "$T/big/audit"
cp "$T/d/drift/terraform_dev.json" "$T/big/drift/"
for i in $(seq 1 1200); do printf '{"ts":"2026-10-%02dT06:%02d:00Z","kind":"apply","status":"applied","workspace":"terraform/dev","actor":"someone","run":"https://github.com/o/r/actions/runs/%d","reviewed":true,"counts":{"add":0,"change":1,"destroy":0,"replace":0},"duration_s":40,"via":"auto-apply"}\n' $((i%27+1)) $((i%60)) "$i"; done > "$T/big/audit/2026-10.jsonl"
REPO=o/r TFPLS_NOW=2026-10-28T10:00:00Z EVENTS=5 bash "$B" "$T/big" "$T/bigout" >"$T/biglog" 2>&1
eq "a 400 KB audit history still builds the page" "$(sed -n '/<script id="data"/,/<\/script>/p' "$T/bigout/index.html" | sed '1s/^<script[^>]*>//;$s/<\/script>$//' | jq -r '.events | length')" 5

done_tests
