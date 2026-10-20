#!/usr/bin/env bash
# SPIKE: the plan store's file format, both ways, against the real openssl path of plan-store.sh and the Go version
# (`tfpls plan-store`). A plan sealed by one must open in the other, and every refusal must read the same in both.
# Needs a binary: TFPLS_PLAN_STORE_BIN=<tfpls>, else `go build` (skipped when there is no go). Run: bash tests/plan-store-compat.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
bin=${TFPLS_PLAN_STORE_BIN:-}
if [ -z "$bin" ]; then
  command -v go >/dev/null 2>&1 || { echo "skip: no go and no TFPLS_PLAN_STORE_BIN"; exit 0; }
  (cd "$here" && go build -o "$T/tfpls" ./cmd/tfpls) || { echo "FAIL go build"; exit 1; }
  bin=$T/tfpls
fi
unset TFPLS_PLAN_STORE_BIN
. "$here/actions/tofu/plan-store.sh"   # the Bash/openssl path
pass=0; failn=0
ok()  { pass=$((pass+1)); echo "ok   $1"; }
bad() { failn=$((failn+1)); echo "FAIL $1"; }

mkdir "$T/w"; head -c 5000 /dev/urandom > "$T/w/tfplan"
echo '{"name":"n","dir":".","tofu_version":"1","tree":"t"}' > "$T/meta.json"
KEY="correct horse battery staple"

# sh_* : the Bash/openssl path; go_* : the Go path. Each leaves its refusal text in ERR and returns the exit status.
sh_pack()   { ( export PLAN_ENCRYPTION_KEY=$1; tfpls_pack "$T/w" "$2" "$T/meta.json" >/dev/null ); }
go_pack()   { ( export PLAN_ENCRYPTION_KEY=$1; "$bin" plan-store pack "$T/w" "$2" "$T/meta.json" >/dev/null ); }
sh_unpack() { ERR=$( ( export PLAN_ENCRYPTION_KEY=$1; tfpls_unpack "$2" "$3" 2>/dev/null || echo "$TFPLS_ERR" >&2 ) 2>&1 >/dev/null ); [ -z "$ERR" ]; }
go_unpack() { ERR=$( ( export PLAN_ENCRYPTION_KEY=$1; "$bin" plan-store unpack "$2" "$3" 2>&1 >/dev/null ) ); [ -z "$ERR" ]; }

# --- sealed by one, opened by the other
sh_pack "$KEY" "$T/s"; go_pack "$KEY" "$T/g"
for who in sh go; do
  src=$T/s; [ "$who" = go ] && src=$T/g
  for opener in sh go; do
    rm -rf "$T/o"; "${opener}_unpack" "$KEY" "$src/plan.tgz.enc" "$T/o" \
      && cmp -s "$T/w/tfplan" "$T/o/tfplan" && cmp -s "$T/meta.json" "$T/o/meta.json" \
      && ok "sealed by $who, opened by $opener, bytes identical" || bad "sealed by $who, opened by $opener ($ERR)"
  done
done

# --- refusals: made from a Go-sealed and from an openssl-sealed file, judged by both openers, texts identical
tamper() { local b; b=$(dd if="$1" bs=1 skip="$2" count=1 2>/dev/null | od -An -tu1 | tr -d ' '); printf "$(printf '\\%03o' $(( b ^ 255 )))" | dd of="$1" bs=1 seek="$2" conv=notrunc 2>/dev/null; }
for who in sh go; do
  src=$T/s; [ "$who" = go ] && src=$T/g
  f=$src/plan.tgz.enc
  cp "$f" "$T/tamper.enc"; tamper "$T/tamper.enc" 200
  head -c 100 "$f" > "$T/trunc.enc"
  head -c 5 "$f" > "$T/tiny.enc"
  printf 'not a plan at all, long enough to look like one......' > "$T/junk.enc"
  "${who}_pack" "" "$T/p$who"
  while read -r label key file; do
    [ "$key" = - ] && key=""
    case "$file" in plain) file=$T/p$who/plan.tgz;; esac
    case "$file" in good) file=$f;; tamper) file=$T/tamper.enc;; trunc) file=$T/trunc.enc;; tiny) file=$T/tiny.enc;; junk) file=$T/junk.enc;; esac
    [ "$key" = wrong ] && key="wrong key"; [ "$key" = right ] && key=$KEY
    sh_unpack "$key" "$file" "$T/r1" && { bad "$label ($who-sealed): openssl path accepted"; continue; }; e1=$ERR
    go_unpack "$key" "$file" "$T/r2" && { bad "$label ($who-sealed): Go accepted"; continue; }; e2=$ERR
    [ -n "$e1" ] && [ "$e1" = "$e2" ] && ok "$label ($who-sealed): both refuse alike: $e1" || bad "$label ($who-sealed): sh='$e1' go='$e2'"
  done <<EOF
wrong-key wrong good
tampered-byte right tamper
truncated right trunc
tiny-file right tiny
not-openssl-data right junk
downgrade-plain-while-key-set right plain
encrypted-without-key - good
EOF
done

# --- the helpers agree too
for t in "" "b.x a.y" "a.y  b.x" "z"; do
  [ "$(tfpls_targets_hash "$t")" = "$("$bin" plan-store targets-hash "$t")" ] && ok "targets-hash '$t' same" || bad "targets-hash '$t'"
done
[ "$(tfpls_plan_name 'x/y z' 0123456789abcdef 'b a')" = "$("$bin" plan-store plan-name 'x/y z' 0123456789abcdef 'b a')" ] && ok "plan-name same" || bad "plan-name"

echo; echo "$pass passed, $failn failed"
[ "$failn" -eq 0 ]
