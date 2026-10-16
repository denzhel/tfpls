#!/usr/bin/env bash
# Parsing helpers for /tofu unlock, sourced by unlock.sh and tests/unlock-parse.test.sh.
# shellcheck disable=SC2034  # the LOCK_* variables are read by the scripts that source this file

# parse_lock "<tofu error text>"  -> sets LOCK_ID, LOCK_WHO, LOCK_OP, LOCK_CREATED; returns 1 if no lock block
parse_lock() {
  local out=$1
  LOCK_ID=""; LOCK_WHO=""; LOCK_OP=""; LOCK_CREATED=""
  grep -q "Error acquiring the state lock" <<<"$out" || return 1
  LOCK_ID=$(sed -n 's/^ *ID: *//p' <<<"$out" | head -1 | tr -d '[:space:]')
  LOCK_WHO=$(sed -n 's/^ *Who: *//p' <<<"$out" | head -1)
  LOCK_OP=$(sed -n 's/^ *Operation: *//p' <<<"$out" | head -1)
  LOCK_CREATED=$(sed -n 's/^ *Created: *//p' <<<"$out" | head -1)
  [[ "$LOCK_ID" =~ ^[0-9A-Za-z-]{8,64}$ ]] || return 2
}

# lock_age_minutes "<Created text, e.g. 2026-10-09 10:00:00.123 +0000 UTC>" [now epoch]  -> minutes, empty if unparsable
lock_age_minutes() {
  local ts epoch now=${2:-$(date -u +%s)}
  ts=$(cut -c1-19 <<<"$1")
  epoch=$(date -u -d "$ts" +%s 2>/dev/null || date -u -j -f '%Y-%m-%d %H:%M:%S' "$ts" +%s 2>/dev/null || echo "")
  [ -n "$epoch" ] && echo $(( (now - epoch) / 60 ))
}
