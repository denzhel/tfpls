#!/usr/bin/env bash
# Names this project defines never start with TF_: that prefix belongs to OpenTofu/Terraform (TF_LOG, TF_VAR_*, TF_WORKSPACE ...).
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$here" || exit 1
pass=0; failn=0
ok()  { pass=$((pass+1)); echo "ok   $1"; }
bad() { failn=$((failn+1)); echo "FAIL $1"; }
# OpenTofu's own variables may be read or set; everything else with this prefix is a clash waiting to happen
allowed='TF_(LOG|LOG_PATH|LOG_CORE|LOG_PROVIDER|INPUT|WORKSPACE|DATA_DIR|IN_AUTOMATION|PLUGIN_CACHE_DIR|CLI_ARGS[A-Z_]*|CLI_CONFIG_FILE|REATTACH_PROVIDERS|VAR_[A-Za-z0-9_]+|TOFU_[A-Z_]*)'
hits=$(grep -rIhoE '\bTF_[A-Z0-9_]+' --exclude-dir=.git --exclude=names.test.sh . | sort -u | grep -vE "^${allowed}\$" || true)
[ -z "$hits" ] && ok "no TF_ name of ours" || bad "TF_ names that may clash with OpenTofu: $hits"
# the product prefix is used instead
grep -rIq 'TFPLS_ROLE_ARN' README.md && ok "the documented variable is TFPLS_ROLE_ARN" || bad "TFPLS_ROLE_ARN not documented"
# nothing of the old names is left
old=$(grep -rIlE 'tf-pipelines|\bTFP_|(^|[^A-Za-z])tfp[-_]' --exclude-dir=.git --exclude=names.test.sh . || true)
[ -z "$old" ] && ok "no trace of the old names" || bad "old names in: $old"
echo; echo "$pass passed, $failn failed"; [ "$failn" -eq 0 ]
