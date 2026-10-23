#!/usr/bin/env bash
# The shell files that remain (the bootstrap, the installer and the wrappers) must run on bash 3.2 (the macOS default, used by self-hosted runners): no mapfile/readarray, no associative arrays,
# no `wait -n`, no `&>>`, no case-changing expansions.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$here" || exit 1
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
hits=$(grep -rnE 'mapfile|readarray|declare -A|local -A|typeset -A|&>>|coproc|wait -n|\$\{[A-Za-z_0-9]+(,,|\^\^)\}' actions/bin/install.sh tools/onboard tools/release.sh tools/tfpls-dashboard 2>/dev/null || true)
[ -z "$hits" ] && ok "no bash 4+ constructs in the remaining shell files" || bad "bash 4+ constructs: $hits"
done_tests
