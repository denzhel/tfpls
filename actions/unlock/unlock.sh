#!/usr/bin/env bash
# Find the lock ID by asking tofu (a plan that fails with "Error acquiring the state lock"),
# then force-unlock exactly that ID. Fails closed: any doubt => refuse and say why.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=parse.sh
. "$here/parse.sh"

label="${LABEL:-$NAME}"

# code of the pull request runs inside these commands (providers, external data sources, modules, policies): they get neither the
# GitHub token nor the plan key. (The cloud credentials stay: tofu needs them; the trust policy of the role is the control there.)
tofu() { env -u GH_TOKEN -u GITHUB_TOKEN -u PLAN_ENCRYPTION_KEY tofu "$@"; }

# remove tofu's working files from the runner workspace when this script ends (self-hosted runners keep it)
cleanup() {
  if [ -n "${REPO_ROOT:-}" ] && [ -n "${DIR:-}" ] && [ -d "$REPO_ROOT/$DIR" ]; then
    ( cd "$REPO_ROOT/$DIR" && rm -rf init.txt unlock.txt .terraform )
  fi
}
trap cleanup EXIT
reply() {
  [ -z "${PR:-}" ] && { echo "$1"; return; }
  gh api "repos/$GITHUB_REPOSITORY/issues/$PR/comments" -f body="$1" >/dev/null
}
# step outputs for callers (the scheduled sweep records them)
# a step output must be ONE line ("name=value"): the active-run message has several lines, so newlines become spaces
out() { [ -n "${GITHUB_OUTPUT:-}" ] && printf '%s=%s\n' "$1" "$(printf '%s' "$2" | tr '\r\n' '  ')" >> "$GITHUB_OUTPUT"; return 0; }
# keep: a lock that is deliberately left alone (fresh, or a run is active)
keep() { refuse "$1"; }
refuse() { out result refused; out detail "$1"; reply "🔒 \`$label\` — not unlocked: $1

[Run logs]($RUN_URL)"; echo "refused: $1"; exit 1; }

cd "$REPO_ROOT/$DIR" || refuse "directory \`$DIR\` not found"
tofu init -input=false -no-color > init.txt 2>&1 || refuse "init failed"
if [ -n "$WS" ]; then
  if ! tofu workspace select "$WS" >/dev/null 2>&1; then
    # a workspace that was never created (new directory, nothing applied yet) has no state and no lock: not a failure when only looking
    if [ "${REPORT_ONLY:-false}" = true ]; then out result not-locked; out detail "workspace \`$WS\` does not exist yet"; echo "not locked: workspace $WS does not exist yet"; exit 0; fi
    refuse "workspace \`$WS\` does not exist"
  fi
fi

# 1. is it locked? `state mv -dry-run` takes the lock for a moment (fails at once when it is held), needs no variables and no
#    providers, and moves nothing: the source address does not exist, so it ends with "Invalid source address" or "No state file"
out=$(tofu state mv -no-color -dry-run -lock-timeout=0s tfpls_probe.a tfpls_probe.b 2>&1)
if ! grep -q "Error acquiring the state lock" <<<"$out"; then
  if grep -qE "No state file was found|Invalid source address|does not match anything" <<<"$out"; then
    out result not-locked; out detail "not locked"
    reply "🔓 \`$label\` is not locked, nothing to do.

[Run logs]($RUN_URL)"; echo "not locked"; exit 0
  fi
  refuse "could not tell whether it is locked (the probe failed for another reason, see the run logs)"
fi

parse_lock "$out"; prc=$?
[ "$prc" -eq 0 ] || refuse "found a lock but could not read its ID"
id=$LOCK_ID; who=$LOCK_WHO; op=$LOCK_OP; created=$LOCK_CREATED
out lock_id "$id"

age=$(lock_age_minutes "$created")
# only look: report who holds the lock and for how long, release nothing (the default of the `locks` task)
if [ "${REPORT_ONLY:-false}" = true ]; then
  out result locked; out detail "held by \`${who:-unknown}\` since ${created%%.*} (${age:-?} min), operation ${op:-unknown}, lock $id"
  echo "locked: $who since $created ($op)"; exit 0
fi

# 2. refuse if the lock is fresh (probably a live run)
[ -n "$age" ] || refuse "could not read the age of the lock ($created), so it is left alone"
[ "$age" -ge 5 ] || refuse "the lock is only ${age} min old (minimum 5); it is probably still in use. Holder: \`$who\`, operation: \`$op\`"

# 3. refuse if any pipeline job for this workspace is still queued or running
active=""
for st in in_progress queued; do
  ids=$(gh api --paginate "repos/$GITHUB_REPOSITORY/actions/runs?status=$st&per_page=100" --jq '.workflow_runs[].id') \
    || refuse "could not list active runs (does the workflow grant \`actions: read\`?)"
  # all pages are read; with a very large number of active runs the answer is "cannot tell", never "nothing is running"
  [ "$(printf '%s\n' "$ids" | grep -c .)" -le 300 ] || refuse "more than 300 runs are active, so it cannot be checked whether one of them holds this lock"
  for rid in $ids; do
    [ "$rid" = "${GITHUB_RUN_ID:-}" ] && continue
    urls=$(gh api --paginate "repos/$GITHUB_REPOSITORY/actions/runs/$rid/jobs?per_page=100" 2>/dev/null \
      | jq -r --arg l "$label" '.jobs[] | select(.status != "completed") | select(.name | (endswith("/ " + $l) or endswith(" (" + $l + ")"))) | .html_url') \
      || refuse "could not inspect run $rid"
    [ -n "$urls" ] && active+="$urls"$'\n'
  done
done
[ -z "$active" ] || keep "a run is still active on this workspace, so the lock is real:
$active"

# 4. release exactly that ID
if tofu force-unlock -force "$id" > unlock.txt 2>&1; then
  out result unlocked; out detail "lock $id held by $who (${age:-?} min old) released"
  reply "🔓 \`$label\` — lock released.

| | |
|---|---|
| Lock ID | \`$id\` |
| Holder | \`$who\` |
| Operation | \`$op\` |
| Taken | $created |

[Run logs]($RUN_URL)"
else
  refuse "\`force-unlock\` failed: $(head -c 500 unlock.txt)"
fi
