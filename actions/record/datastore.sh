#!/usr/bin/env bash
# KEPT ONLY for finish/store.sh and drift-report until they are ported (Go: internal/datastore). Shared by record.sh and finish/store.sh: change files on the data branch (an orphan branch, created on first
# use) and push, starting over from the new tip when another run won the race.
#   tfpls_data_write <writer> <commit message>
# <writer> is a function that gets the clone directory and edits files in it; nothing to commit is a success.
# Returns 0 when pushed (or nothing changed), 1 when it could not be done. Test overrides: TFPLS_REMOTE_URL, TFPLS_RETRY_SLEEP.
tfpls_data_write() {
  local writer=$1 msg=$2 attempt d
  local branch="${BRANCH:-tfpls-data}"
  local remote="${TFPLS_REMOTE_URL:-https://x-access-token:${TOKEN:-}@github.com/${REPO:-}.git}"
  d=$(mktemp -d)
  # a lost race starts over from the new tip: 10 attempts with a growing random pause (parallel drift legs and applies share ONE ref)
  for attempt in 1 2 3 4 5 6 7 8 9 10; do
    rm -rf "$d"; mkdir -p "$d"
    if ! git clone --quiet --depth 1 --branch "$branch" "$remote" "$d" 2>/dev/null; then
      rm -rf "$d"; mkdir -p "$d"
      git init --quiet "$d" && git -C "$d" checkout --quiet -b "$branch" && git -C "$d" remote add origin "$remote" || { rm -rf "$d"; return 1; }
    fi
    "$writer" "$d" || { rm -rf "$d"; return 1; }
    git -C "$d" add -A >/dev/null 2>&1
    if git -C "$d" diff --cached --quiet; then rm -rf "$d"; return 0; fi
    git -C "$d" -c user.name=tfpls -c user.email=tfpls@users.noreply.github.com commit --quiet -m "$msg" >/dev/null 2>&1 || { rm -rf "$d"; return 1; }
    if git -C "$d" push --quiet origin "$branch" >/dev/null 2>&1; then rm -rf "$d"; return 0; fi
    [ "$attempt" -lt 10 ] || break
    sleep "${TFPLS_RETRY_SLEEP:-$((1 + RANDOM % (3 + 2 * attempt)))}"
  done
  rm -rf "$d"; return 1
}
