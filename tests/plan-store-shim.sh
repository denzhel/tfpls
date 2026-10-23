#!/usr/bin/env bash
# Reviewed-plan store: pack/unpack a saved tofu plan, name its artifact, find it, validate it.
# The store itself is Go (internal/planstore, `tfpls plan-store`): this file only keeps the function names run.sh and the tests
# use, until the tofu action itself is Go. Sourced by run.sh and the tests. The functions never `exit`; they return non-zero and
# set TFPLS_ERR to the human-readable reason; the result of tfpls_pack/lookup/fetch is on stdout and in TFPLS_OUT.
# File format (plan.tgz.enc, when PLAN_ENCRYPTION_KEY is set): AES-256-CBC (PBKDF2, salted) over "<64 hex sha256 of tgz>\n<tgz>";
# plan.tgz otherwise. The key is only ever passed through the environment, never on a command line.
# The binary is `tfpls` on the PATH (the tofu action installs it first), or TFPLS_PLAN_STORE_BIN.

# shellcheck disable=SC2034  # TFPLS_ERR and TFPLS_OUT are read by the scripts that source this file
TFPLS_ERR=""
TFPLS_OUT=""

# _tfpls_go <op> <args...>   runs the binary, keeps its reason in TFPLS_ERR, echoes its result
_tfpls_go() {
  local rc ef out
  ef=$(mktemp)
  out=$("${TFPLS_PLAN_STORE_BIN:-tfpls}" plan-store "$@" 2>"$ef"); rc=$?
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
