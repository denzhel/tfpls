#!/usr/bin/env bash
# A FIFO per workspace without cancelling anybody (GitHub's own concurrency groups keep one running and one pending run
# and silently cancel the rest).  Every job takes a numbered ticket (a file on the data branch; the first push wins the
# number, so numbers are strictly ordered) and waits until no earlier ticket belongs to a run that is still active.
#   enter  take a ticket, then wait for the turn (prints the position, comments on the PR when PR is set)
#   leave  remove my ticket (run it with `if: always()`); a ticket whose run has finished is ignored by the others anyway,
#          so a crashed or cancelled holder never blocks the queue.
# Test overrides: TFPLS_REMOTE_URL, TFPLS_POLL (seconds between polls, default 10).
# the wait budget: never the whole job timeout (a job that waited its full budget would be killed mid-apply holding the lock)
budget() { local t=${1:-50}; [[ "$t" =~ ^[0-9]+$ ]] || t=50; if [ "$t" -gt 30 ]; then echo $((t - 15)); elif [ "$t" -ge 2 ]; then echo $((t / 2)); else echo "$t"; fi; }
set -uo pipefail
MODE="${MODE:?mode}"; NAME="${NAME:?name}"
BRANCH="${BRANCH:-tfpls-data}"
REMOTE="${TFPLS_REMOTE_URL:-https://x-access-token:${TOKEN:-}@github.com/${REPO:-}.git}"
POLL="${TFPLS_POLL:-10}"
label="${LABEL:-$NAME}"
safe=$(printf '%s' "$NAME" | tr '/ ' '__' | tr -cd 'A-Za-z0-9._-')
qdir="queue/$safe"
me="${RUN_ID:-0}-${RUN_ATTEMPT:-1}-$$"
state="${RUNNER_TEMP:-/tmp}/tfpls-queue-$safe-${RUN_ID:-0}"       # remembers my ticket file between the enter and leave steps
case "$BRANCH" in main|master) echo "::warning::queue: refusing to use \`$BRANCH\`"; exit 0;; esac
[[ "$BRANCH" =~ ^[A-Za-z0-9._/-]+$ ]] || { echo "::warning::queue: invalid branch name"; exit 0; }

d=$(mktemp -d); trap 'rm -rf "$d"' EXIT
git_() { git -C "$d" -c user.name=tfpls -c user.email=tfpls@users.noreply.github.com "$@"; }
fresh() { # a new shallow clone of the data branch (or an empty repository when the branch does not exist yet)
  rm -rf "$d"; mkdir -p "$d"
  if ! git clone --quiet --depth 1 --branch "$BRANCH" "$REMOTE" "$d" 2>/dev/null; then
    rm -rf "$d"; mkdir -p "$d"
    git init --quiet "$d" && git_ checkout --quiet -b "$BRANCH" && git_ remote add origin "$REMOTE" || return 1
  fi
}
tickets() { find "$d/$qdir" -maxdepth 1 -type f -name '[0-9]*-*.json' -exec basename {} \; 2>/dev/null | sort; }       # zero-padded sequence first: lexical order = queue order
run_active() { # is workflow run $1 still queued or running?
  local st; st=$(gh api "repos/$REPO/actions/runs/$1" --jq .status 2>/dev/null) || return 0   # cannot tell: assume it is
  [ -n "$st" ] && [ "$st" != completed ]
}

case "$MODE" in
enter)
  my=""
  for attempt in 1 2 3 4 5 6 7 8 9 10; do
    fresh || { echo "::warning::queue: git setup failed, continuing without a queue"; exit 0; }
    mkdir -p "$d/$qdir"
    last=$(tickets | tail -1 | cut -d- -f1); n=$(( 10#${last:-0} + 1 ))
    my=$(printf '%08d-%s.json' "$n" "$me")
    jq -n --arg run "${RUN_ID:-}" --arg job "$label" --arg pr "${PR:-}" --arg url "${RUN_URL:-}" '{run:$run,job:$job,pr:$pr,url:$url}' > "$d/$qdir/$my"
    [ -f "$d/README.md" ] || printf '# tfpls data\n\nQueues (`queue/`), audit log and drift state written by tfpls.\n' > "$d/README.md"
    git_ add -A >/dev/null 2>&1
    git_ commit --quiet -m "queue: $NAME ticket $n" >/dev/null 2>&1 || { my=""; break; }
    if git_ push --quiet origin "$BRANCH" >/dev/null 2>&1; then break; fi
    my=""; sleep "${TFPLS_RETRY_SLEEP:-$((1 + RANDOM % (3 + 2 * attempt)))}"
  done
  [ -n "$my" ] || { echo "::warning::queue: could not take a ticket, continuing without waiting"; exit 0; }
  echo "$my" > "$state"
  echo "queue: took ticket $my for $label"
  WAIT_MIN=$(budget "${TIMEOUT:-50}")
  deadline=$(( $(date +%s) + WAIT_MIN * 60 )); announced=""
  while :; do
    fresh || true
    ahead=0; first=""
    for t in $(tickets); do
      [ "$t" = "$my" ] && break
      r=$(jq -r '.run // empty' "$d/$qdir/$t" 2>/dev/null)
      if [ -n "$r" ] && [ "$r" != "${RUN_ID:-}" ] && run_active "$r"; then ahead=$((ahead + 1)); [ -n "$first" ] || first=$(jq -r '.url // empty' "$d/$qdir/$t"); fi
    done
    if [ "$ahead" -eq 0 ]; then
      echo "queue: it is our turn"
      [ -n "$announced" ] && [ -n "${PR:-}" ] && gh api -X DELETE "repos/$REPO/issues/comments/$announced" >/dev/null 2>&1
      exit 0
    fi
    echo "queue: position $((ahead + 1)) for $label ($ahead ahead, waiting for ${first:-another run})"
    if [ -n "${PR:-}" ]; then
      body="⏳ \`$label\` is queued: position $((ahead + 1)) ($ahead ahead). It starts automatically when ${first:+[the run in front]($first) is}finished. [This run]($RUN_URL)"
      if [ -z "$announced" ]; then
        announced=$(gh api "repos/$REPO/issues/$PR/comments" -f body="$body" --jq .id 2>/dev/null || true)
      else
        gh api -X PATCH "repos/$REPO/issues/comments/$announced" -f body="$body" >/dev/null 2>&1 || true
      fi
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      echo "::error::gave up waiting in the queue for \`$label\` after $WAIT_MIN minutes (position $((ahead + 1)))"
      [ -n "$announced" ] && gh api -X DELETE "repos/$REPO/issues/comments/$announced" >/dev/null 2>&1
      exit 1
    fi
    sleep "$POLL"
  done;;
leave)
  my=$(cat "$state" 2>/dev/null || true)
  [ -n "$my" ] || { echo "queue: no ticket to give up"; exit 0; }
  for attempt in 1 2 3 4 5 6 7 8 9 10; do
    fresh || exit 0
    [ -f "$d/$qdir/$my" ] || { echo "queue: ticket already gone"; rm -f "$state"; exit 0; }
    git_ rm --quiet "$qdir/$my" >/dev/null 2>&1
    git_ commit --quiet -m "queue: $NAME done" >/dev/null 2>&1 || exit 0
    if git_ push --quiet origin "$BRANCH" >/dev/null 2>&1; then echo "queue: ticket released"; rm -f "$state"; exit 0; fi
    sleep "${TFPLS_RETRY_SLEEP:-$((1 + RANDOM % (3 + 2 * attempt)))}"
  done
  echo "::warning::queue: could not remove the ticket (later runs ignore it once this run has finished)";;
*) echo "unknown mode: $MODE" >&2; exit 2;;
esac
