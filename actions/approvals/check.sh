#!/usr/bin/env bash
# check.sh <reviews.json> <head-sha> <strict|basic> <required>
# Decide whether the reviews of a pull request satisfy the approval policy for /tofu apply.
#   strict: only approvals given for the CURRENT head commit count (every new commit needs a new review)
#   basic : an approval counts whatever commit it was given on (the latest review of each person still wins)
# In both modes a person's latest review decides (approve / request changes / dismissed) and any outstanding
# "changes requested" blocks. Prints "ok" or the refusal reason; exit 0 = satisfied, 1 = refused, 2 = usage.
set -uo pipefail
reviews=${1:-}; sha=${2:-}; mode=${3:-}; need=${4:-}
[ -f "$reviews" ] && [ -n "$sha" ] && [[ "$mode" =~ ^(strict|basic)$ ]] && [[ "$need" =~ ^[0-9]+$ ]] || { echo "usage: check.sh <reviews.json> <sha> <strict|basic> <required>" >&2; exit 2; }
[ "$need" -eq 0 ] && { echo ok; exit 0; }

read -r approved_head approved_old changes <<<"$(jq -r --arg sha "$sha" '
  [ .[] | select(.state == "APPROVED" or .state == "CHANGES_REQUESTED" or .state == "DISMISSED") ]
  | group_by(.user.login) | map(sort_by(.submitted_at) | last)
  | [ ([.[] | select(.state == "APPROVED" and .commit_id == $sha)] | length),
      ([.[] | select(.state == "APPROVED" and .commit_id != $sha)] | length),
      ([.[] | select(.state == "CHANGES_REQUESTED")] | length) ] | map(tostring) | join(" ")' "$reviews")"

if [ "$mode" = strict ]; then
  if [ "$changes" -gt 0 ]; then echo "a reviewer requested changes; apply needs $need approving review(s) of the current commit"; exit 1; fi
  if [ "$approved_head" -lt "$need" ]; then
    if [ "$approved_old" -gt 0 ]; then echo "the approval is for an older commit: new commits were pushed after the review. Ask for a new review of ${sha:0:7}"; exit 1; fi
    echo "apply needs $need approving review(s) of the current commit (${sha:0:7}); found $approved_head"; exit 1
  fi
else
  total=$((approved_head + approved_old))
  if [ "$changes" -gt 0 ]; then echo "a reviewer requested changes; apply needs $need approving review(s)"; exit 1; fi
  if [ "$total" -lt "$need" ]; then echo "apply needs $need approving review(s); found $total"; exit 1; fi
fi
echo ok
