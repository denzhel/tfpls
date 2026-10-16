#!/usr/bin/env bash
# actions/unlock/sweep.sh (the manual `locks` task): reports every workspace's lock, releases only workspaces named in RELEASE.
# Stubbed tofu and gh (no network).
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SW="$here/actions/unlock/sweep.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin" "$T/repo/a" "$T/repo/b" "$T/repo/c"
# a directory holding a file `lock` is locked since the time written in it; a file `broken` makes init fail
cat > "$T/bin/tofu" <<'STUB'
#!/usr/bin/env bash
case "$1" in
  init) [ -f broken ] && exit 1; exit 0;;
  workspace) exit 0;;
  state) if [ -f lock ]; then printf 'Error: Error acquiring the state lock\n\nLock Info:\n  ID:        a1b2c3d4-e5f6-7890-abcd-ef1234567890\n  Path:      s/x\n  Operation: OperationTypeApply\n  Who:       ci@host\n  Version:   1.10.6\n  Created:   %s\n' "$(cat lock)"; exit 1
         else printf 'Error: Invalid source address\n'; exit 1; fi;;
  force-unlock) echo "$PWD" >> "$STUB_LOG"; exit 0;;
esac
STUB
printf '#!/usr/bin/env bash\nexit 0\n' > "$T/bin/gh"
chmod +x "$T/bin/"*
ago() { date -u -v-"$1"M '+%Y-%m-%d %H:%M:%S.000 +0000 UTC' 2>/dev/null || date -u -d "$1 minutes ago" '+%Y-%m-%d %H:%M:%S.000 +0000 UTC'; }

entries='{"a":{"name":"s/a","dir":"a","workspace":""},"b":{"name":"s/b","dir":"b","workspace":""},"c":{"name":"s/c","dir":"c","workspace":""}}'
run() { : > "$T/log"; : > "$T/gho"; : > "$T/summary"
  PATH="$T/bin:$PATH" STUB_LOG="$T/log" GITHUB_OUTPUT="$T/gho" GITHUB_STEP_SUMMARY="$T/summary" ENTRIES="$entries" RELEASE="${RELEASE:-}" REPO_ROOT="$T/repo" RUN_URL=u GITHUB_REPOSITORY=o/r GITHUB_RUN_ID=999 bash "$SW" >"$T/out" 2>&1; echo $?; }
out() { sed -n "s/^$1=//p" "$T/gho"; }

eq "no locks: exit 0, nothing locked or released" "$(run)|$(out locked)|$(out released)" "0|0|0"
grep -q "| \`a\` | free |" "$T/summary" && ok "the summary table lists every workspace" || bad "summary: $(cat "$T/summary")"
ago90=$(ago 90); echo "$ago90" > "$T/repo/a/lock"; echo "$ago90" > "$T/repo/c/lock"
eq "two locked workspaces are reported, nothing is released" "$(run)|$(out locked)|$(out released)|$(grep -c . "$T/log")" "0|2|0|0"
grep -q "ci@host" "$T/summary" && grep -q "locked" "$T/summary" && ok "the table says who holds the lock" || bad "holder missing: $(cat "$T/summary")"
grep -q "Nothing is released automatically" "$T/summary" && ok "and how to release one" || bad "no hint"
eq "a named workspace is released, the other locked one is not" "$(RELEASE="c" run)|$(out released)|$(sed 's|.*/||' "$T/log" | tr '\n' ',')" "0|1|c,"
echo "$(ago 2)" > "$T/repo/a/lock"
eq "a lock younger than 5 minutes is not released even when named (the run fails with the reason)" "$(RELEASE="a" run)|$(grep -c . "$T/log")" "1|0"
eq "names match the label or the name" "$(echo "$(ago 90)" > "$T/repo/a/lock"; RELEASE="s/a" run)|$(sed 's|.*/||' "$T/log" | tr '\n' ',')" "0|a,"
touch "$T/repo/b/broken"; rm -f "$T/repo/a/lock" "$T/repo/c/lock"
eq "a workspace that cannot be checked fails the run but the others are still checked" "$(run)|$(grep -c '| `a` | free' "$T/summary")|$(grep -c 'could not check' "$T/summary")" "1|1|1"

done_tests
