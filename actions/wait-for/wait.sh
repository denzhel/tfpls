#!/usr/bin/env bash
# Poll this run's jobs until every named predecessor has completed; stop (fail) if one of them did not succeed.
# A job is found by name: the display name of a matrix job from a reusable workflow ends in "/ <label>".
# Test overrides: TFPLS_POLL (seconds between polls, default 10).
set -uo pipefail
# the wait budget: never the whole job timeout (a job that waited its full budget would be killed mid-apply holding the lock)
budget() { local t=${1:-50}; [[ "$t" =~ ^[0-9]+$ ]] || t=50; if [ "$t" -gt 30 ]; then echo $((t - 15)); elif [ "$t" -ge 2 ]; then echo $((t / 2)); else echo "$t"; fi; }
POLL="${TFPLS_POLL:-10}"
WAIT_MIN=$(budget "${TIMEOUT:-50}")
deadline=$(( $(date +%s) + WAIT_MIN * 60 ))
IFS=',' read -r -a want <<<"$WAIT_FOR"
echo "waiting for: ${want[*]}"
while :; do
  jobs_json=$(gh api "repos/$REPO/actions/runs/$RUN_ID/jobs?per_page=100" --paginate --jq '.jobs[] | {name, status, conclusion}' 2>/dev/null | jq -sc '.') \
    || jobs_json=""
  if [ -z "$jobs_json" ]; then
    echo "::warning::could not read the jobs of this run (does the workflow grant \`actions: read\`?), retrying"
  else
    pending=""
    for w in "${want[@]}"; do
      w="${w// /}"; [ -n "$w" ] || continue
      row=$(jq -c --arg w "$w" '[.[] | select(.name == $w or (.name | endswith("/ " + $w)) or (.name | endswith(" (" + $w + ")")))] | .[0] // empty' <<<"$jobs_json")
      if [ -z "$row" ]; then pending+="$w (not started) "; continue; fi
      st=$(jq -r .status <<<"$row"); cc=$(jq -r '.conclusion // ""' <<<"$row")
      if [ "$st" != completed ]; then pending+="$w ($st) "; continue; fi
      if [ "$cc" != success ]; then
        echo "::error::not applied: \`$w\` has to succeed first and finished as $cc"
        exit 1
      fi
    done
    [ -z "$pending" ] && { echo "all predecessors succeeded"; exit 0; }
    echo "still waiting: $pending"
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then echo "::error::gave up waiting for predecessors after $WAIT_MIN minutes"; exit 1; fi
  sleep "$POLL"
done
