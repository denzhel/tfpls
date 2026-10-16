#!/usr/bin/env bash
# Fill the settings of one workspace from its matrix entry (sourced by run.sh and the tests).

# tfpls_load_entry: when ENTRY (JSON of the entry from detect-workspaces) is set, every setting variable that is empty
# gets its value from it. Explicit values win (drift passes its own auto_apply).
tfpls_load_entry() {
  [ -n "${ENTRY:-}" ] || return 0
  local var key val
  while read -r var key; do
    [ -z "${!var:-}" ] || continue
    val=$(jq -r --arg k "$key" '.[$k] // "" | tostring' <<<"$ENTRY" 2>/dev/null) || val=""
    printf -v "$var" '%s' "$val"
  done <<'MAP'
NAME name
LABEL label
DIR dir
WS workspace
VAR_FILE var_file
PARALLELISM parallelism
PLAN_LOCK plan_lock
LOCK_TIMEOUT lock_timeout
CREATE_WS create_workspace
AUTO_APPLY auto_apply
POLICY_DIR policy_dir
POLICY_PACKS policy_packs
POLICY_PARAMS policy_params
MAP
}
