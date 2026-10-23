#!/usr/bin/env bash
# Offline tests for the plan store (the functions of tests/plan-store-shim.sh, answered by `tfpls plan-store`; no network, no tofu). Run: bash tests/plan-store.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
. "$here/plan-store-shim.sh"
export DEFAULT_BRANCH=main
export GITHUB_REPOSITORY_ID=100   # the id of the repository whose own plans are trusted
pass=0; failn=0
ok()   { pass=$((pass+1)); echo "ok   $1"; }
bad()  { failn=$((failn+1)); echo "FAIL $1"; }
expect_ok()   { local n=$1; shift; if "$@" >/dev/null 2>&1; then ok "$n"; else bad "$n"; fi; }
expect_fail() { local n=$1; shift; if "$@" >/dev/null 2>&1; then bad "$n"; else ok "$n"; fi; }
contains()    { case "$2" in *"$3"*) ok "$1";; *) bad "$1 (got: $2)";; esac; }

T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
# the store is Go: build the binary that the functions of plan-store.sh call
. "$here/lib.sh"
build_bin tfpls "$T/tfpls" || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
export TFPLS_PLAN_STORE_BIN="$T/tfpls"
mkdir "$T/w"; head -c 4096 /dev/urandom > "$T/w/tfplan"
cat > "$T/meta.json" <<J
{"name":"terraform/prod-us-west-1","dir":".","tofu_version":"1.10.6","tree":"abc123def456","head_sha":"deadbeefdeadbeef","targets":""}
J

# ---- naming
[ "$(tfpls_slug 'terraform/prod-us-west-1')" = "terraform__prod-us-west-1" ] && ok "slug replaces /" || bad "slug"
[ "$(tfpls_slug 'a b;c')" = "a_b_c" ] && ok "slug sanitizes" || bad "slug sanitize"
[ "$(tfpls_targets_hash '')" = all ] && ok "no targets => all" || bad "targets all"
[ "$(tfpls_targets_hash 'b.x a.y')" = "$(tfpls_targets_hash 'a.y b.x')" ] && ok "targets order-insensitive" || bad "targets order"
[ "$(tfpls_targets_hash 'a.y')" != "$(tfpls_targets_hash 'a.z')" ] && ok "different targets differ" || bad "targets differ"
[ "$(tfpls_plan_name 'x/y' 0123456789abcdef '')" = "tfpls-plan-x__y-0123456789ab-all" ] && ok "plan name" || bad "plan name: $(tfpls_plan_name 'x/y' 0123456789abcdef '')"

# ---- encrypted round trip
export PLAN_ENCRYPTION_KEY="correct horse battery staple"
f=$(tfpls_pack "$T/w" "$T/o1" "$T/meta.json"); case "$f" in *.enc) ok "pack with key => .enc";; *) bad "pack with key";; esac
expect_fail "encrypted file is not a tar" tar -tzf "$f"
expect_ok "unpack roundtrip" tfpls_unpack "$f" "$T/u1"
cmp -s "$T/w/tfplan" "$T/u1/tfplan" && ok "plan bytes identical" || bad "plan bytes differ"
# wrong key
( export PLAN_ENCRYPTION_KEY="wrong"; tfpls_unpack "$f" "$T/u2" ) && bad "wrong key accepted" || ok "wrong key refused"
# tampered byte: tamper_byte writes the complement of the byte, so the file ALWAYS changes (a fixed value would leave the file
# untouched whenever that byte of the random ciphertext already has it, about 1 run in 256)
tamper_byte() { # <file> <offset>
  local b; b=$(dd if="$1" bs=1 skip="$2" count=1 2>/dev/null | od -An -tu1 | tr -d ' ')
  printf "$(printf '\\%03o' $(( b ^ 255 )))" | dd of="$1" bs=1 seek="$2" conv=notrunc 2>/dev/null
}
for v in '\x00' '\xff' '\x5a'; do
  printf "$v$v$v" > "$T/h.bin"; cp "$T/h.bin" "$T/h.orig"; tamper_byte "$T/h.bin" 1
  cmp -s "$T/h.bin" "$T/h.orig" && bad "tamper_byte left a file with the byte $v unchanged" || ok "tamper_byte changes a byte that is $v"
done
cp "$f" "$T/tampered.enc"; tamper_byte "$T/tampered.enc" 200
tfpls_unpack "$T/tampered.enc" "$T/u3" >/dev/null 2>&1 && bad "tampered file accepted" || ok "tampered file refused"
# truncated
head -c 100 "$f" > "$T/trunc.enc"; tfpls_unpack "$T/trunc.enc" "$T/u4" >/dev/null 2>&1 && bad "truncated accepted" || ok "truncated refused"
# .enc without key
( unset PLAN_ENCRYPTION_KEY; tfpls_unpack "$f" "$T/u5" ); rc=$?
[ $rc -ne 0 ] && ok ".enc without key refused" || bad ".enc without key"
# ---- plain
plain=$( unset PLAN_ENCRYPTION_KEY; tfpls_pack "$T/w" "$T/o2" "$T/meta.json" ); case "$plain" in *plan.tgz) ok "pack without key => plain";; *) bad "pack plain: $plain";; esac
( unset PLAN_ENCRYPTION_KEY; tfpls_unpack "$plain" "$T/u6" ) && ok "plain unpack without key" || bad "plain unpack"
tfpls_unpack "$plain" "$T/u7" >/dev/null 2>&1 && bad "downgrade (plain while key set) accepted" || ok "downgrade refused"
# error text
tfpls_unpack "$plain" "$T/u8" >/dev/null 2>&1; contains "downgrade message" "$TFPLS_ERR" "downgrade"
( unset PLAN_ENCRYPTION_KEY; tfpls_unpack "$f" "$T/u9" >/dev/null 2>&1; echo "$TFPLS_ERR" > "$T/err" ); contains "needs-key message" "$(cat "$T/err")" "PLAN_ENCRYPTION_KEY is not set"
# secrets must not appear on a command line: the pack path passes the key only via env
grep -q 'pass:' "$here/plan-store-shim.sh" "$here/../internal/planstore/planstore.go" && bad "key passed via pass: (argv)" || ok "key never in argv (-pass env: only)"

# ---- meta checks
chk() { tfpls_check_meta "$T/meta.json" "$@"; }
expect_ok   "meta matches"            chk abc123def456 terraform/prod-us-west-1 . 1.10.6
expect_fail "tree mismatch refused"   chk 000000000000 terraform/prod-us-west-1 . 1.10.6
tfpls_check_meta "$T/meta.json" 000000000000 terraform/prod-us-west-1 . 1.10.6; contains "tree message" "$TFPLS_ERR" "different code"
expect_fail "workspace mismatch"      chk abc123def456 terraform/dev . 1.10.6
expect_fail "dir mismatch"            chk abc123def456 terraform/prod-us-west-1 other 1.10.6
expect_fail "tofu version mismatch"   chk abc123def456 terraform/prod-us-west-1 . 1.11.0

# ---- regression: TFPLS_ERR must survive (callers must not lose it in a subshell)
fakebin=$(mktemp -d); printf '#!/bin/sh\nexit 1\n' > "$fakebin/gh"; chmod +x "$fakebin/gh"
PATH="$fakebin:$PATH" tfpls_fetch o/r missing-artifact "$T/dl" >/dev/null 2>&1; rc=$?
[ "$rc" -eq 2 ] && ok "fetch failure returns 2" || bad "fetch rc=$rc"
contains "fetch error text set (no subshell)" "$TFPLS_ERR" "could not list artifacts"
printf '#!/bin/sh\necho ""\n' > "$fakebin/gh"
PATH="$fakebin:$PATH" tfpls_fetch o/r missing-artifact "$T/dl" >/dev/null 2>&1; rc=$?
[ "$rc" -eq 1 ] && ok "missing artifact returns 1" || bad "missing rc=$rc"
contains "missing artifact text" "$TFPLS_ERR" "no reviewed plan found"
rm -rf "$fakebin"
p2=$( unset PLAN_ENCRYPTION_KEY; tfpls_pack "$T/w" "$T/o3" "$T/meta.json" >/dev/null; echo "$TFPLS_OUT" ); case "$p2" in *plan.tgz) ok "TFPLS_OUT set by pack";; *) bad "TFPLS_OUT pack: $p2";; esac

# ---- expiry vs missing: a stub gh that returns an artifact list
fb2=$(mktemp -d); cat > "$fb2/gh" <<'STUB'
#!/bin/sh
printf '%s' "$FAKE_ARTIFACTS"
STUB
chmod +x "$fb2/gh"
lookup_with() { DEFAULT_BRANCH=main PATH="$fb2:$PATH" FAKE_ARTIFACTS="$1" tfpls_lookup o/r some-plan; }
EXPIRED='{"artifacts":[{"id":1,"expired":true,"created_at":"2026-01-01T00:00:00Z","expires_at":"2026-01-15T00:00:00Z","workflow_run":{"id":11,"repository_id":100,"head_repository_id":100,"head_branch":"main"}}]}'
LIVE='{"artifacts":[{"id":2,"expired":false,"created_at":"2026-02-01T00:00:00Z","expires_at":"2026-02-15T00:00:00Z","workflow_run":{"id":22,"repository_id":100,"head_repository_id":100,"head_branch":"main"}}]}'
BOTH='{"artifacts":[{"id":1,"expired":true,"created_at":"2026-01-01T00:00:00Z","expires_at":"2026-01-15T00:00:00Z","workflow_run":{"id":11,"repository_id":100,"head_repository_id":100,"head_branch":"main"}},{"id":2,"expired":false,"created_at":"2026-02-01T00:00:00Z","expires_at":"2026-02-15T00:00:00Z","workflow_run":{"id":22,"repository_id":100,"head_repository_id":100,"head_branch":"main"}}]}'
EMPTY='{"artifacts":[]}'
out=$(lookup_with "$EXPIRED"); rc=$?; lookup_with "$EXPIRED" >/dev/null; err=$TFPLS_ERR
[ $rc -eq 1 ] && ok "only an expired plan => not found (rc 1)" || bad "expired rc=$rc"
contains "expired plan is reported as expired, with the date" "$err" "expired on 2026-01-15"
lookup_with "$EMPTY" >/dev/null; contains "no artifact at all => 'no reviewed plan found'" "$TFPLS_ERR" "no reviewed plan found"
out=$(lookup_with "$BOTH"); [ "$out" = "2 22" ] && ok "a live plan wins over an expired one" || bad "both: '$out'"
out=$(lookup_with "$LIVE"); [ "$out" = "2 22" ] && ok "live plan is found" || bad "live: '$out'"

# ---- hints: each refusal reason gets a specific fix
hint() { tfpls_hint "$1" prod-us-west-1 "${2:-}"; }
contains "hint: missing key => pass the secret to this workflow" "$(hint 'the saved plan is encrypted but PLAN_ENCRYPTION_KEY is not set')" "secrets: inherit"
contains "hint: wrong key => same key in plan and apply" "$(hint 'could not decrypt the saved plan (wrong key or corrupted file)')" "same PLAN_ENCRYPTION_KEY"
contains "hint: integrity failure => same advice" "$(hint 'the saved plan failed its integrity check (wrong key or tampered)')" "same PLAN_ENCRYPTION_KEY"
contains "hint: plain plan with a key => plan again" "$(hint 'the saved plan is NOT encrypted but PLAN_ENCRYPTION_KEY is set; refusing a possible downgrade')" "saved encrypted"
contains "hint: missing permission => actions: read" "$(hint "could not list artifacts (does the workflow grant 'actions: read'?)")" "actions: read"
contains "hint: expired => retention" "$(hint 'the reviewed plan expired on 2026-01-15 (x)')" "14 days"
contains "hint: different code => update the branch" "$(hint 'the reviewed plan was made for different code (plan tree a, current tree b)')" "Update the branch"
contains "hint: tofu version change" "$(hint 'the reviewed plan was made with tofu 1.9.0, this run uses 1.10.6; run /tofu plan again')" "version changed"
contains "hint: default suggests the plan command" "$(hint 'no reviewed plan found (x)')" "/tofu plan prod-us-west-1"
contains "hint: the plan command repeats the -target values" "$(hint 'no reviewed plan found (x)' 'module.vpc aws_x.y')" "-target=module.vpc -target=aws_x.y"

# ---- provenance: only artifacts of this repository's own runs
FOREIGN='{"artifacts":[{"id":9,"expired":false,"created_at":"2026-03-01T00:00:00Z","workflow_run":{"id":99,"repository_id":100,"head_repository_id":200,"head_branch":"main"}},{"id":2,"expired":false,"created_at":"2026-02-01T00:00:00Z","workflow_run":{"id":22,"repository_id":100,"head_repository_id":100,"head_branch":"main"}}]}'
out=$(lookup_with "$FOREIGN"); [ "$out" = "2 22" ] && ok "a newer artifact from a fork's run loses" || bad "foreign artifact won: '$out'"
ONLYFOREIGN='{"artifacts":[{"id":9,"expired":false,"created_at":"2026-03-01T00:00:00Z","workflow_run":{"id":99,"repository_id":100,"head_repository_id":200,"head_branch":"main"}}]}'
lookup_with "$ONLYFOREIGN" >/dev/null; rc=$?; contains "only a fork's artifact: no plan found" "$TFPLS_ERR" "no reviewed plan found"; [ "$rc" -eq 1 ] && ok "only a fork's artifact: rc 1" || bad "only foreign rc=$rc"
NULLIDS='{"artifacts":[{"id":9,"expired":false,"created_at":"2026-03-01T00:00:00Z","workflow_run":{"id":99}},{"id":8,"expired":false,"created_at":"2026-03-02T00:00:00Z"}]}'
lookup_with "$NULLIDS" >/dev/null; rc=$?; [ "$rc" -eq 1 ] && ok "artifacts without repository ids are ignored (null is not equal to the id)" || bad "null ids rc=$rc"
out=$(unset GITHUB_REPOSITORY_ID; PATH="$fb2:$PATH" FAKE_ARTIFACTS="$LIVE" tfpls_lookup o/r some-plan; echo "rc=$?"); case "$out" in *rc=2) ok "an unknown repository id refuses";; *) bad "unknown id: $out";; esac
# many foreign artifacts must not hide the real one: the listing is paginated with 100 per page
fb3=$(mktemp -d); cat > "$fb3/gh" <<'STUB'
#!/bin/sh
echo "$*" >> "$GH_LOG"
printf '%s' "$FAKE_ARTIFACTS"
STUB
chmod +x "$fb3/gh"
many='{"artifacts":['
for i in $(seq 0 39); do many="$many{\"id\":$((1000 + i)),\"expired\":false,\"created_at\":\"2026-03-01T00:00:00Z\",\"workflow_run\":{\"id\":$((5000 + i)),\"repository_id\":100,\"head_repository_id\":200,\"head_branch\":\"main\"}},"; done
many="$many{\"id\":2,\"expired\":false,\"created_at\":\"2026-02-01T00:00:00Z\",\"workflow_run\":{\"id\":22,\"repository_id\":100,\"head_repository_id\":100,\"head_branch\":\"main\"}}]}"
T_LOG=$(mktemp)
out=$(GH_LOG="$T_LOG" PATH="$fb3:$PATH" FAKE_ARTIFACTS="$many" tfpls_lookup o/r some-plan)
[ "$out" = "2 22" ] && ok "the real plan is found behind 40 foreign artifacts" || bad "behind 40 foreign: '$out'"
grep -q -- "--paginate" "$T_LOG" && grep -q "per_page=100" "$T_LOG" && ok "the artifact listing is paginated with 100 per page" || bad "no pagination: $(cat "$T_LOG")"

# the producing run must be trusted code: default branch, or exactly the commit being applied
BR='{"artifacts":[{"id":9,"expired":false,"created_at":"2026-03-01T00:00:00Z","workflow_run":{"id":99,"repository_id":100,"head_repository_id":100,"head_branch":"mallory","head_sha":"evil"}},{"id":2,"expired":false,"created_at":"2026-02-01T00:00:00Z","workflow_run":{"id":22,"repository_id":100,"head_repository_id":100,"head_branch":"pr-1","head_sha":"abc"}}]}'
out=$(HEAD_SHA=abc lookup_with "$BR"); [ "$out" = "2 22" ] && ok "a newer plan from another branch's run loses to the run of the applied commit" || bad "branch plant won: '$out'"
HEAD_SHA=zzz lookup_with "$BR" >/dev/null; rc=$?; [ "$rc" -eq 1 ] && ok "no run of the applied commit and none on the default branch: no plan" || bad "untrusted rc=$rc"

rm -rf "$fb2" "$fb3"

echo; echo "$pass passed, $failn failed"
[ "$failn" -eq 0 ]
