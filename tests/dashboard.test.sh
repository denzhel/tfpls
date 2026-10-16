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


# insights (30 days before 2026-10-10T10:00:00Z; the fixture has 2 applied + 1 audit-only apply)
eq "applies are counted over 30 days" "$(data | jq -c '[.insights.applies,.insights.applied,.insights.failed]')" "[3,3,0]"
eq "success rate" "$(data | jq -r '.insights.success_rate')" 100
eq "reviewed share counts reviewed plans among applied" "$(data | jq -r '.insights.reviewed_share')" 33
eq "average and slowest apply duration" "$(data | jq -c '[.insights.avg_apply_seconds,.insights.slowest_apply.seconds]')" "[40,40]"
eq "busiest workspace" "$(data | jq -r '.insights.busiest[0].workspace')" "terraform/dev"
eq "a recently applied in-sync workspace is not unused" "$(data | jq -r '.insights.unused|length')" 0
mkdir -p "$T/u/drift" "$T/u/audit"
echo '{"name":"t/idle","state":"in-sync","summary":""}' > "$T/u/drift/t_idle.json"
echo '{"name":"t/old","state":"in-sync","summary":""}' > "$T/u/drift/t_old.json"
echo '{"name":"t/busy","state":"in-sync","summary":""}' > "$T/u/drift/t_busy.json"
echo '{"name":"t/drifted","state":"drift","summary":""}' > "$T/u/drift/t_drifted.json"
printf '%s\n' '{"ts":"2026-05-01T00:00:00Z","kind":"apply","status":"applied","workspace":"t/old"}' '{"ts":"2026-10-09T00:00:00Z","kind":"apply","status":"applied","workspace":"t/busy"}' '{"ts":"2026-10-09T01:00:00Z","kind":"apply","status":"error","workspace":"t/busy"}' > "$T/u/audit/2026-10.jsonl"
REPO=o/r TFPLS_NOW=2026-10-10T10:00:00Z bash "$B" "$T/u" "$T/uo" >/dev/null 2>&1
udata() { sed -n '/<script id="data"/,/<\/script>/p' "$T/uo/index.html" | sed '1s/^<script[^>]*>//;$s/<\/script>$//'; }
eq "in-sync workspaces without a recent apply are listed (never applied, applied long ago)" "$(udata | jq -r '[.insights.unused[].workspace]|sort|join(",")')" "t/idle,t/old"
eq "a drifted workspace is not called unused" "$(udata | jq -r '[.insights.unused[].workspace]|index("t/drifted")')" "null"
eq "failed applies lower the success rate" "$(udata | jq -r '.insights.success_rate')" 50

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
