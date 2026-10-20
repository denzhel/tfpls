#!/usr/bin/env bash
# SPIKE shim: the plan-store functions of plan-store.sh, answered by `tfpls plan-store` (the Go version).
# plan-store.sh sources this file last when TFPLS_PLAN_STORE_BIN names the binary, so every caller (run.sh, tools/tfpls-plan,
# the tests) runs unchanged against Go. Same contract: stdout is the result, TFPLS_OUT/TFPLS_ERR are set in the caller's shell.

# shellcheck disable=SC2034  # TFPLS_ERR and TFPLS_OUT are read by the scripts that source plan-store.sh

# _tfpls_go <op> <args...>   runs the binary, keeps its reason in TFPLS_ERR, echoes its result
_tfpls_go() {
  local rc ef out
  ef=$(mktemp)
  out=$("$TFPLS_PLAN_STORE_BIN" plan-store "$@" 2>"$ef"); rc=$?
  [ "$rc" -eq 0 ] || TFPLS_ERR=$(cat "$ef")
  rm -f "$ef"
  [ -z "$out" ] || printf '%s\n' "$out"
  _TFPLS_GO_RESULT=$out
  return "$rc"
}

tfpls_slug() { _tfpls_go slug "$1"; }
tfpls_targets_hash() { _tfpls_go targets-hash "$1"; }
tfpls_plan_name() { _tfpls_go plan-name "$1" "$2" "$3"; }
tfpls_check_meta() { _tfpls_go check-meta "$@"; }
tfpls_hint() { _tfpls_go hint "$1" "$2" "${3:-}"; }
tfpls_unpack() { _tfpls_go unpack "$1" "$2"; }
tfpls_pack() { _tfpls_go pack "$@" && TFPLS_OUT=$_TFPLS_GO_RESULT; }
tfpls_lookup() { _tfpls_go lookup "$@" && TFPLS_OUT=$_TFPLS_GO_RESULT; }
tfpls_fetch() { _tfpls_go fetch "$@" && TFPLS_OUT=$_TFPLS_GO_RESULT; }
