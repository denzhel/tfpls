#!/usr/bin/env bash
# actions/tofu/entry.sh: the matrix entry fills the settings that were not passed explicitly.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/actions/tofu/entry.sh"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

E='{"name":"terraform/dev","label":"dev","dir":".","workspace":"dev","var_file":"w/dev.tfvars","parallelism":"7","plan_lock":"false","lock_timeout":"2m","auto_apply":"always","policy_dir":"policies","policy_packs":"safety,aws-tags","policy_params":"{\"safety\":{}}"}'
load() { ( eval "export $1"; ENTRY="$E"; tfpls_load_entry; eval "$2" ); }
eq "name, label, dir and workspace come from the entry" "$(load 'NAME= LABEL= DIR= WS=' 'echo "$NAME|$LABEL|$DIR|$WS"')" "terraform/dev|dev|.|dev"
eq "settings come from the entry" "$(load 'PARALLELISM= PLAN_LOCK= LOCK_TIMEOUT= VAR_FILE=' 'echo "$PARALLELISM|$PLAN_LOCK|$LOCK_TIMEOUT|$VAR_FILE"')" "7|false|2m|w/dev.tfvars"
eq "policy settings come from the entry" "$(load 'POLICY_DIR= POLICY_PACKS=' 'echo "$POLICY_DIR|$POLICY_PACKS"')" "policies|safety,aws-tags"
eq "an explicit value wins over the entry (drift passes its own auto_apply)" "$(load 'AUTO_APPLY=additive_only' 'echo "$AUTO_APPLY"')" "additive_only"
eq "the entry fills auto_apply when nothing was passed" "$(load 'AUTO_APPLY=' 'echo "$AUTO_APPLY"')" "always"
eq "no entry: nothing changes" "$( ( NAME=x DIR=d; ENTRY=""; tfpls_load_entry; echo "$NAME|$DIR|${LABEL:-}" ) )" "x|d|"
eq "a missing key leaves the variable empty" "$( ( ENTRY='{"name":"a"}'; PARALLELISM=; tfpls_load_entry; echo "[${PARALLELISM}]" ) )" "[]"
# shellcheck disable=SC2034  # ENTRY is read by tfpls_load_entry
eq "invalid JSON is ignored" "$( ( ENTRY='not json'; NAME=; tfpls_load_entry; echo "[${NAME}]" ) )" "[]"

done_tests
