#!/usr/bin/env bash
# The scripts must run on bash 3.2 (the macOS default, used by self-hosted runners): no mapfile/readarray, no associative arrays,
# no `wait -n`, no `&>>`, no case-changing expansions.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$here" || exit 1
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
hits=$(grep -rnE 'mapfile|readarray|declare -A|local -A|typeset -A|&>>|coproc|wait -n|\$\{[A-Za-z_0-9]+(,,|\^\^)\}' actions tools 2>/dev/null | grep -v '^actions/dashboard/template.html' | grep -v '^tools/release.sh.*#' || true)
[ -z "$hits" ] && ok "no bash 4+ constructs in actions/ and tools/" || bad "bash 4+ constructs: $hits"
done_tests
