#!/usr/bin/env bash
# Secrets reach a job only by name. A computed lookup (`secrets[<expression>]`) or the whole context (`toJSON(secrets)`) makes
# GitHub hand EVERY secret the workflow can see to the runner, and with `secrets: inherit` that is every repository and
# organisation secret of the caller (CodeQL actions/excessive-secrets-exposure). Every reusable workflow declares exactly the
# secrets it names under on.workflow_call.secrets (tofu.yml: those of the workflows it calls), so a caller can list them.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
W="$here/.github/workflows"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT

# the expressions of a workflow or action file as "<line>:<text>": every ${{ ... }} and every bare `if:` condition
# (comment lines are skipped)
exprs() {
  awk '/^[[:space:]]*#/ { next }
    { s = $0
      if (match(s, /^[[:space:]]*(-[[:space:]]+)?if:[[:space:]]*/)) print NR ":" substr(s, RSTART + RLENGTH)
      while (match(s, /[$][{][{]([^}]|[}][^}])*[}][}]/)) { print NR ":" substr(s, RSTART, RLENGTH); s = substr(s, RSTART + RLENGTH) } }' "$1"
}
# the secrets context used as anything but secrets.NAME
whole() { grep -iE '(^|[^.[:alnum:]_-])secrets([^.[:alnum:]_-]|$)'; }
# the secret names a file uses (secrets.NAME) and the ones it declares, sorted, on one line
named() { exprs "$1" | grep -oiE '(^|[^.[:alnum:]_-])secrets[.][[:alnum:]_]+' | sed -E 's/.*[.]//' | tr '[:lower:]' '[:upper:]' | LC_ALL=C sort -u | tr '\n' ' '; }
declared() { yq -r '.on.workflow_call.secrets // {} | keys | .[]' "$1" | tr '[:lower:]' '[:upper:]' | LC_ALL=C sort -u | tr '\n' ' '; }

# ---- the check itself: it finds every form, and nothing in a file that names its secrets
cat > "$T/bad.yml" <<'Y'
jobs:
  a:
    steps:
      - with:
          webhook: ${{ secrets[fromJSON(needs.d.outputs.entries)[matrix.label].notify_secret || 'SLACK_WEBHOOK'] }}
          all: ${{ toJSON(secrets) }}
          spaced: ${{ tojson( SECRETS ) }}
          literal: ${{ secrets['SLACK_WEBHOOK'] }}
          passed: x-${{ inputs.a }}-${{ format('{0}', secrets) }}
      - if: secrets[inputs.name] != ''
Y
cat > "$T/good.yml" <<'Y'
# ${{ secrets[inputs.name] }} in a comment is not an expression
jobs:
  a:
    secrets: inherit
    steps:
      - if: inputs.secrets_dir != '' && needs.secrets.outputs.ok == 'true'
        with:
          webhook: ${{ secrets.SLACK_WEBHOOK }}
          group: x${{ inputs.queue && format('-{0}', github.run_id) || '' }}-${{ secrets.TFPLS_NOTIFY_WEBHOOKS }}
          note: "composite actions cannot read secrets"
Y
eq "the check finds a computed lookup and the whole context in every form" "$(exprs "$T/bad.yml" | whole | cut -d: -f1 | tr '\n' ' ')" "5 6 7 8 9 10 "
eq "and nothing in a file that names its secrets" "$(exprs "$T/good.yml" | whole | wc -l | tr -d ' ')" 0
eq "it reads the names a file uses" "$(named "$T/good.yml")" "SLACK_WEBHOOK TFPLS_NOTIFY_WEBHOOKS "

# ---- the workflows and the composite actions
n=0; found=""
for f in "$W"/*.yml "$here"/actions/*/action.yml; do
  n=$((n + 1))
  hits=$(exprs "$f" | whole)
  [ -z "$hits" ] || found="$found$(sed "s|^|  ${f#"$here"/}:|" <<<"$hits")"$'\n'
done
eq "the scan covers the workflows and the actions" "$([ "$n" -ge 30 ] && echo yes || echo "only $n files")" yes
if [ -z "$found" ]; then ok "no computed secrets[...] lookup and no whole secrets context (toJSON(secrets)) in the workflows and actions"
else bad "a secret looked up by a computed name, or the whole secrets context, hands EVERY secret to the runner; use secrets.NAME:"; printf '%s' "$found"; fi

# ---- declarations: exactly the secrets a reusable workflow names, so a caller that lists its secrets sees which ones
for f in "$W"/*.yml; do
  [ "$(yq -r '.on.workflow_call != null' "$f")" = true ] || continue
  [ "${f##*/}" = tofu.yml ] && continue
  eq "${f##*/} declares exactly the secrets it uses" "$(declared "$f")" "$(named "$f")"
done
callees=$(yq -r '.jobs[].uses // "" | select(test("^denzhel/tfpls/[.]github/workflows/")) | sub("^denzhel/tfpls/[.]github/workflows/"; "") | sub("@.*"; "")' "$W/tofu.yml" | LC_ALL=C sort -u)
eq "tofu.yml calls the reusable workflows" "$([ "$(grep -c . <<<"$callees")" -ge 5 ] && echo yes)" yes
want=$(for c in $callees; do named "$W/$c"; done | tr ' ' '\n' | grep . | LC_ALL=C sort -u | tr '\n' ' ')
eq "tofu.yml declares exactly the secrets of the workflows it calls (a caller may list them instead of secrets: inherit)" "$(declared "$W/tofu.yml")" "$want"
eq "those are the four documented secrets" "$want" "PLAN_ENCRYPTION_KEY SLACK_WEBHOOK TFPLS_ENV_JSON TFPLS_NOTIFY_WEBHOOKS "

# ---- the jobs that notify pass the two webhook secrets to finish, by name
m=0
for f in "$W"/*.yml; do
  grep -q 'uses: denzhel/tfpls/actions/finish@' "$f" || continue
  m=$((m + 1))
  eq "${f##*/}: finish gets the webhook secrets by name" \
    "$(yq -r '[.jobs[].steps[]? | select((.uses // "") | test("^denzhel/tfpls/actions/finish@")) | (.with.webhook // "") + " " + (.with.webhooks // "")] | unique | join("|")' "$f")" \
    '${{ secrets.SLACK_WEBHOOK }} ${{ secrets.TFPLS_NOTIFY_WEBHOOKS }}'
done
eq "four workflows notify (apply, comment-ops, drift, drift-fix)" "$m" 4

done_tests
