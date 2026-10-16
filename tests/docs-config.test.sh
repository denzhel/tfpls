#!/usr/bin/env bash
# The reference must name every setting the config validator knows, and its documented defaults must equal the entry workflow's.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$here" || exit 1
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

keys=$(grep -E '^def (settings|stack_keys):' actions/detect-workspaces/validate.jq | grep -oE '"[a-z_]+"' | tr -d '"' | sort -u)
[ -n "$keys" ] && ok "found $(wc -l <<<"$keys" | tr -d ' ') settings in validate.jq" || bad "no settings found in validate.jq"
for k in $keys; do
  grep -q "\`$k\`" docs/reference.md && ok "reference names $k" || bad "reference does not name the setting $k"
done

# default of an entry-workflow input vs the reference table row for it
wf_default() { yq -r ".on.workflow_call.inputs.$1.default" .github/workflows/tofu.yml; }
readme_default() { grep -E "^\| \`$1\`" docs/reference.md | head -1 | awk -F'|' '{print $3}' | tr -d '` '; }
for i in audit drift_issues runs_on queue; do
  w=$(wf_default "$i"); r=$(readme_default "$i")
  [ "$w" = "$r" ] && ok "reference default of $i is $w" || bad "reference default of $i is '$r', the entry workflow has '$w'"
done
# the reference's "Workflow inputs" table is exactly the inputs of tofu.yml, with the same defaults (both ways)
sec=$(awk '/^## Workflow inputs/{f=1;next} /^## /{f=0} f' docs/reference.md)
readme_inputs=$(grep -E '^\| `[a-z_]+` \|' <<<"$sec" | sed -E 's/^\| `([a-z_]+)` \|.*/\1/' | sort | tr '\n' ' ')
wf_inputs=$(yq -r '.on.workflow_call.inputs | keys | .[]' .github/workflows/tofu.yml | sort | tr '\n' ' ')
[ "$readme_inputs" = "$wf_inputs" ] && ok "reference inputs table == tofu.yml inputs ($wf_inputs)" || bad "reference inputs [$readme_inputs] differ from tofu.yml [$wf_inputs]"
for i in $(yq -r '.on.workflow_call.inputs | keys | .[]' .github/workflows/tofu.yml); do
  w=$(wf_default "$i"); [ -n "$w" ] || { case $i in tofu_version) w=detected;; *) w=none;; esac; }
  r=$(grep -E "^\| \`$i\` \|" <<<"$sec" | awk -F'|' '{print $3}' | tr -d '` ')
  [ "$w" = "$r" ] && ok "reference default of $i is $w" || bad "reference default of $i is '$r', tofu.yml has '$w'"
done
done_tests
