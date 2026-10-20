#!/usr/bin/env bash
# Put `tfpls` on the PATH of the job.
#   VERSION next to this file is `dev` on main and branches, and the exact tag (vX.Y.Z[-rc.N]) in a release (tools/release.sh).
#   A tag: download tfpls_<os>_<arch>.tar.gz of that release, check it against SHA256SUMS and its build provenance, cache it.
#   dev:   build from the source of this very checkout (the code of the ref that is being run), with the runner's Go.
# Env (tests): TFPLS_RELEASE_BASE, TFPLS_TOOL_CACHE, TFPLS_RETRY_DELAY, TFPLS_OS, TFPLS_ARCH, TFPLS_VERSION_FILE.
set -euo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
slug=${GITHUB_ACTION_REPOSITORY:-denzhel/tfpls}
ver=$(tr -d '[:space:]' < "${TFPLS_VERSION_FILE:-$here/VERSION}")
base=${TFPLS_RELEASE_BASE:-https://github.com/$slug/releases/download}
cache=${TFPLS_TOOL_CACHE:-${RUNNER_TOOL_CACHE:-$HOME/.cache}/tfpls}
delay=${TFPLS_RETRY_DELAY:-3}

case "${TFPLS_OS:-$(uname -s)}" in Linux|linux) os=linux;; Darwin|darwin) os=darwin;; *) echo "::error::unsupported OS for the tfpls binary" >&2; exit 1;; esac
case "${TFPLS_ARCH:-$(uname -m)}" in x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; *) echo "::error::unsupported architecture for the tfpls binary" >&2; exit 1;; esac

export_path() { if [ -n "${GITHUB_PATH:-}" ]; then echo "$1" >> "$GITHUB_PATH"; fi; echo "tfpls $("$1/tfpls" version) is on the PATH ($1)"; }

if [ "$ver" = dev ]; then
  command -v go >/dev/null 2>&1 || { echo "::error::this ref is not a release, so tfpls is built from source, and there is no Go on this runner (use a release tag, or install Go)" >&2; exit 1; }
  dest=${RUNNER_TEMP:-${TMPDIR:-/tmp}}/tfpls-bin
  mkdir -p "$dest"
  echo "::notice::building tfpls from the source of this ref (a release tag installs a verified binary instead)"
  ( cd "$here/../.." && CGO_ENABLED=0 GOTOOLCHAIN=local go build -trimpath -ldflags "-s -w -X main.version=dev" -o "$dest/tfpls" ./cmd/tfpls )
  export_path "$dest"; exit 0
fi

[[ "$ver" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]] || { echo "::error::bad tfpls version '$ver' in actions/bin/VERSION" >&2; exit 1; }
dest=$cache/bin/$ver/$os-$arch
if [ -x "$dest/tfpls" ]; then export_path "$dest"; exit 0; fi

sha256_of() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
fetch() { # url out: curl's retries inside a few attempts of ours
  local n=1
  until curl -fsSL --connect-timeout 20 --max-time 300 --retry 3 --retry-delay "$delay" --retry-all-errors -o "$2" "$1"; do
    [ "$n" -lt 4 ] || { echo "::error::download failed after 4 attempts: $1" >&2; return 1; }
    echo "::warning::download failed (attempt $n of 4), retrying: $1" >&2; sleep $((delay * n)); n=$((n + 1))
  done
}

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
file=tfpls_${os}_${arch}.tar.gz
fetch "$base/$ver/$file" "$tmp/$file"; fetch "$base/$ver/SHA256SUMS" "$tmp/sums"
want=$(awk -v f="$file" '$2 == f || $2 == "*" f {print $1; exit}' "$tmp/sums")
[ -n "$want" ] || { echo "::error::no checksum for $file in the $ver release" >&2; exit 1; }
have=$(sha256_of "$tmp/$file")
[ "$want" = "$have" ] || { echo "::error::checksum mismatch for $file (expected $want, got $have)" >&2; exit 1; }

# provenance: the archive must come from this repository's release workflow (a checksum alone only proves the download)
if command -v gh >/dev/null 2>&1 && [ -n "${GH_TOKEN:-${GITHUB_TOKEN:-}}" ]; then
  gh attestation verify "$tmp/$file" --repo "$slug" --signer-workflow "$slug/.github/workflows/release.yml" >/dev/null 2>&1 \
    || { echo "::error::the build provenance of $file could not be verified" >&2; exit 1; }
else
  echo "::warning::gh or a token is missing: only the SHA-256 of tfpls $ver was verified, not its build provenance"
fi

mkdir -p "$tmp/x" "$dest"
tar -xzf "$tmp/$file" -C "$tmp/x" tfpls
cp "$tmp/x/tfpls" "$dest/tfpls.$$" && chmod +x "$dest/tfpls.$$" && mv -f "$dest/tfpls.$$" "$dest/tfpls"
export_path "$dest"
