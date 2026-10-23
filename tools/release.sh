#!/usr/bin/env bash
# Cut a release (see docs/releasing.md). The logic is Go: cmd/release.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
exec go run "$root/cmd/release" "$@"
