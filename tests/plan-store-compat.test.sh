#!/usr/bin/env bash
# The plan store's file format, both ways: the reference openssl implementation below (what the Bash plan store did before the
# Go port) against `tfpls plan-store`. A plan sealed by one must open in the other, and every refusal must read the same in both.
# Run: bash tests/plan-store-compat.test.sh   (TFPLS_PLAN_STORE_BIN=<tfpls> to use a built binary)
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
bin=${TFPLS_PLAN_STORE_BIN:-}
if [ -z "$bin" ]; then
  build_bin tfpls "$T/tfpls" || { echo "FAIL go build ./cmd/tfpls"; exit 1; }
  bin=$T/tfpls
fi

sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum | cut -d' ' -f1; else shasum -a 256 | cut -d' ' -f1; fi; }

# ---- the reference: the former Bash/openssl functions (pack, unpack), kept here as the format's definition
ref_pack() { # <key> <dir> <outdir> <meta>
  local tmp; tmp=$(mktemp -d); mkdir -p "$3"
  cp "$2/tfplan" "$tmp/tfplan"; cp "$4" "$tmp/meta.json"; tar -C "$tmp" -czf "$tmp/plan.tgz" tfplan meta.json
  if [ -n "$1" ]; then
    { sha < "$tmp/plan.tgz"; cat "$tmp/plan.tgz"; } | PLAN_ENCRYPTION_KEY=$1 openssl enc -aes-256-cbc -pbkdf2 -md sha256 -salt -pass env:PLAN_ENCRYPTION_KEY -out "$3/plan.tgz.enc"
  else cp "$tmp/plan.tgz" "$3/plan.tgz"; fi
  rm -rf "$tmp"
}
ref_unpack() { # <key> <file> <outdir>: the reason on stderr
  local key=$1 file=$2 out=$3 tmp; tmp=$(mktemp -d); mkdir -p "$out"
  case "$file" in
    *.enc)
      [ -n "$key" ] || { echo "the saved plan is encrypted but PLAN_ENCRYPTION_KEY is not set" >&2; return 1; }
      PLAN_ENCRYPTION_KEY=$key openssl enc -d -aes-256-cbc -pbkdf2 -md sha256 -pass env:PLAN_ENCRYPTION_KEY -in "$file" -out "$tmp/dec" 2>/dev/null \
        || { echo "could not decrypt the saved plan (wrong key or corrupted file)" >&2; return 1; }
      local want have; want=$(head -c 64 "$tmp/dec"); tail -c +66 "$tmp/dec" > "$tmp/plan.tgz"; have=$(sha < "$tmp/plan.tgz")
      [ "$want" = "$have" ] || { echo "the saved plan failed its integrity check (wrong key or tampered)" >&2; return 1; };;
    *.tgz)
      [ -z "$key" ] || { echo "the saved plan is NOT encrypted but PLAN_ENCRYPTION_KEY is set; refusing a possible downgrade (run /tofu plan again)" >&2; return 1; }
      cp "$file" "$tmp/plan.tgz";;
  esac
  tar -C "$out" -xzf "$tmp/plan.tgz" tfplan meta.json 2>/dev/null || { echo "the saved plan archive is invalid" >&2; return 1; }
}

mkdir "$T/w"; head -c 5000 /dev/urandom > "$T/w/tfplan"
echo '{"name":"n","dir":".","tofu_version":"1","tree":"t"}' > "$T/meta.json"
KEY="correct horse battery staple"

# sh_* : the reference path; go_* : the Go path. Each leaves its refusal text in ERR and returns the exit status.
sh_pack()   { ref_pack "$1" "$T/w" "$2" "$T/meta.json"; }
go_pack()   { ( export PLAN_ENCRYPTION_KEY=$1; "$bin" plan-store pack "$T/w" "$2" "$T/meta.json" >/dev/null ); }
sh_unpack() { ERR=$(ref_unpack "$1" "$2" "$3" 2>&1 >/dev/null); [ -z "$ERR" ]; }
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
    case "$file" in plain) file=$T/p$who/plan.tgz;; good) file=$f;; tamper) file=$T/tamper.enc;; trunc) file=$T/trunc.enc;; tiny) file=$T/tiny.enc;; junk) file=$T/junk.enc;; esac
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

# --- a plain plan is a plain tgz that tar reads, and a member named like the plan but nested is not extracted
sh_pack "" "$T/ps"; go_pack "" "$T/pg"
tar -tzf "$T/pg/plan.tgz" | tr '\n' ' ' | grep -q '^tfplan meta.json $' && ok "the Go plain archive lists tfplan and meta.json" || bad "plain archive members"
rm -rf "$T/o"; go_unpack "" "$T/ps/plan.tgz" "$T/o" && cmp -s "$T/w/tfplan" "$T/o/tfplan" && ok "Go opens a plain archive made by tar" || bad "Go opens a tar-made plain archive ($ERR)"

# --- the helpers: the former Bash definitions
slug() { printf '%s' "$1" | sed 's#/#__#g; s#[^A-Za-z0-9._-]#_#g'; }
targets_hash() { local t; t=$(printf '%s\n' $1 | LC_ALL=C sort | tr '\n' ' ' | sed 's/ *$//'); if [ -z "$t" ]; then echo all; else printf '%s' "$t" | sha | cut -c1-8; fi; }
for t in "" "b.x a.y" "a.y  b.x" "z"; do
  eq "targets-hash '$t' same" "$("$bin" plan-store targets-hash "$t")" "$(targets_hash "$t")"
done
eq "plan-name same" "$("$bin" plan-store plan-name 'x/y z' 0123456789abcdef 'b a')" "tfpls-plan-$(slug 'x/y z')-0123456789ab-$(targets_hash 'b a')"

done_tests
