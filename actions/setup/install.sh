#!/usr/bin/env bash
# Install OpenTofu and conftest from GitHub releases, robustly:
#   - every download is retried with backoff (a transient network error must not fail a run),
#   - every archive is verified against the SHA-256 listed in the release's checksum file,
#   - tools are cached under the runner's tool cache, so a self-hosted runner downloads each version once.
# Inputs (env): TOFU_VERSION, CONFTEST_VERSION. Test overrides: TFPLS_TOFU_BASE_URL, TFPLS_CONFTEST_BASE_URL,
# TFPLS_TOOL_CACHE, TFPLS_RETRY_DELAY (seconds), TFPLS_OS, TFPLS_ARCH.
set -euo pipefail

TOFU_VERSION="${TOFU_VERSION:-1.10.6}"
CONFTEST_VERSION="${CONFTEST_VERSION:-0.56.0}"
TOFU_BASE="${TFPLS_TOFU_BASE_URL:-https://github.com/opentofu/opentofu/releases/download}"
CONFTEST_BASE="${TFPLS_CONFTEST_BASE_URL:-https://github.com/open-policy-agent/conftest/releases/download}"
CACHE="${TFPLS_TOOL_CACHE:-${RUNNER_TOOL_CACHE:-${HOME}/.cache}/tfpls}"
DELAY="${TFPLS_RETRY_DELAY:-3}"
GH_PATH="${GITHUB_PATH:-/dev/null}"

case "${TFPLS_OS:-$(uname -s)}" in Linux|linux) os=linux; Os=Linux;; Darwin|darwin) os=darwin; Os=Darwin;; *) echo "::error::unsupported OS" >&2; exit 1;; esac
case "${TFPLS_ARCH:-$(uname -m)}" in x86_64|amd64) arch=amd64; carch=x86_64;; aarch64|arm64) arch=arm64; carch=arm64;; *) echo "::error::unsupported architecture" >&2; exit 1;; esac

sha256_of() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

# fetch <url> <out>: curl's own retries, wrapped in a few attempts of our own with growing backoff
fetch() {
  local url=$1 out=$2 n=1 max=4
  while :; do
    if curl -fsSL --connect-timeout 20 --max-time 300 --retry 3 --retry-delay "$DELAY" --retry-all-errors -o "$out" "$url"; then return 0; fi
    if [ "$n" -ge "$max" ]; then echo "::error::download failed after $max attempts: $url" >&2; return 1; fi
    echo "::warning::download failed (attempt $n of $max), retrying: $url" >&2
    sleep $((DELAY * n)); n=$((n + 1))
  done
}

# install_tool <name> <version> <archive url> <checksums url> <archive file name> <binary inside>
install_tool() {
  local name=$1 ver=$2 url=$3 sums_url=$4 file=$5 bin=$6
  local dest="$CACHE/$name/$ver/$os-$arch"
  if [ -x "$dest/$bin" ]; then
    echo "$name $ver: cached at $dest"
  else
    local tmp; tmp=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf '$tmp'" RETURN
    fetch "$url" "$tmp/$file"
    fetch "$sums_url" "$tmp/sums"
    local want have
    want=$(awk -v f="$file" '$2 == f || $2 == "*" f {print $1; exit}' "$tmp/sums")
    [ -n "$want" ] || { echo "::error::no checksum for $file in $sums_url" >&2; return 1; }
    have=$(sha256_of "$tmp/$file")
    if [ "$want" != "$have" ]; then echo "::error::checksum mismatch for $file (expected $want, got $have)" >&2; return 1; fi
    mkdir -p "$tmp/x"
    case "$file" in
      *.zip)    unzip -q -o "$tmp/$file" -d "$tmp/x";;
      *.tar.gz) tar -xzf "$tmp/$file" -C "$tmp/x";;
      *) echo "::error::unknown archive type: $file" >&2; return 1;;
    esac
    [ -f "$tmp/x/$bin" ] || { echo "::error::$bin not found in $file" >&2; return 1; }
    mkdir -p "$dest"
    # move into place atomically enough for concurrent runners sharing the cache
    cp "$tmp/x/$bin" "$dest/$bin.$$" && chmod +x "$dest/$bin.$$" && mv -f "$dest/$bin.$$" "$dest/$bin"
    echo "$name $ver: installed to $dest (checksum verified)"
  fi
  echo "$dest" >> "$GH_PATH"
}

install_tool opentofu "$TOFU_VERSION" \
  "$TOFU_BASE/v$TOFU_VERSION/tofu_${TOFU_VERSION}_${os}_${arch}.zip" \
  "$TOFU_BASE/v$TOFU_VERSION/tofu_${TOFU_VERSION}_SHA256SUMS" \
  "tofu_${TOFU_VERSION}_${os}_${arch}.zip" tofu

if [ "${INSTALL_CONFTEST:-true}" = false ]; then echo "conftest: skipped (no policy configured)"; else
install_tool conftest "$CONFTEST_VERSION" \
  "$CONFTEST_BASE/v$CONFTEST_VERSION/conftest_${CONFTEST_VERSION}_${Os}_${carch}.tar.gz" \
  "$CONFTEST_BASE/v$CONFTEST_VERSION/checksums.txt" \
  "conftest_${CONFTEST_VERSION}_${Os}_${carch}.tar.gz" conftest
fi
