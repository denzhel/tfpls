#!/usr/bin/env bash
# Tests for actions/bin/install.sh: a fabricated release on disk (file://), a fake gh. Run: bash tests/bin-install.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
. "$here/lib.sh"
inst="$here/../actions/bin/install.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
V=v9.9.9
os=linux; arch=amd64   # forced through TFPLS_OS/TFPLS_ARCH: the fabricated release carries the host binary under this name

# a release: tfpls (built for the host, version stamped) packed as tfpls_linux_amd64.tar.gz, plus SHA256SUMS
rel="$T/rel/$V"; mkdir -p "$rel" "$T/b" "$T/fakebin"
(cd "$here/.." && go build -o "$T/b/tfpls" -ldflags "-X main.version=$V" ./cmd/tfpls) || { echo "FAIL go build"; exit 1; }
tar -czf "$rel/tfpls_${os}_${arch}.tar.gz" -C "$T/b" tfpls
sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
printf '%s  %s\n' "$(sha "$rel/tfpls_${os}_${arch}.tar.gz")" "tfpls_${os}_${arch}.tar.gz" > "$rel/SHA256SUMS"
printf '%s\n' "$V" > "$T/VERSION"
printf '#!/bin/sh\nexit 0\n' > "$T/fakebin/gh-ok"; printf '#!/bin/sh\nexit 1\n' > "$T/fakebin/gh-bad"; chmod +x "$T/fakebin/"*

# run <name> [env...]: fresh cache and PATH file per call unless TFPLS_TOOL_CACHE is given; sets rc and out
run() { # gh-mode then env assignments
  local gh=$1; shift
  rm -rf "$T/path-bin"; mkdir -p "$T/path-bin"
  [ "$gh" = none ] || ln -s "$T/fakebin/gh-$gh" "$T/path-bin/gh"
  : > "$T/ghpath"
  out=$(env -i PATH="$T/path-bin:/usr/bin:/bin:/usr/local/bin:$(dirname "$(command -v curl)")" HOME="$T/home" GH_TOKEN=t GITHUB_PATH="$T/ghpath" \
        TFPLS_VERSION_FILE="$T/VERSION" TFPLS_RELEASE_BASE="file://$T/rel" TFPLS_OS=$os TFPLS_ARCH=$arch TFPLS_RETRY_DELAY=0 "$@" bash "$inst" 2>&1); rc=$?
}
pathdir() { head -1 "$T/ghpath"; }

run ok TFPLS_TOOL_CACHE="$T/c1"
[ $rc -eq 0 ] && ok "a release binary installs" || bad "install failed: $out"
[ "$("$(pathdir)/tfpls" version 2>/dev/null)" = "$V" ] && ok "the installed binary is the release's (version $V) and its directory is on the PATH" || bad "wrong binary on the PATH"

run ok TFPLS_TOOL_CACHE="$T/c1" TFPLS_RELEASE_BASE="file://$T/nowhere"
[ $rc -eq 0 ] && ok "the second run uses the cache (no download)" || bad "cache not used: $out"

# a corrupted checksum is refused and nothing is cached
cp "$rel/SHA256SUMS" "$T/sums.good"; sed '1s/^./0/' "$T/sums.good" > "$rel/SHA256SUMS"
run ok TFPLS_TOOL_CACHE="$T/c2"
[ $rc -ne 0 ] && grep -q "checksum mismatch" <<<"$out" && [ ! -e "$T/c2/bin" ] && ok "a checksum mismatch is refused and nothing is cached" || bad "mismatch accepted: $out"
printf '%s  other.tar.gz\n' "$(sha "$rel/tfpls_${os}_${arch}.tar.gz")" > "$rel/SHA256SUMS"
run ok TFPLS_TOOL_CACHE="$T/c2"
[ $rc -ne 0 ] && grep -q "no checksum for" <<<"$out" && ok "a missing checksum entry is refused" || bad "missing entry accepted: $out"
cp "$T/sums.good" "$rel/SHA256SUMS"

# a tampered archive is refused
cp "$rel/tfpls_${os}_${arch}.tar.gz" "$T/arc.good"; printf 'x' >> "$rel/tfpls_${os}_${arch}.tar.gz"
run ok TFPLS_TOOL_CACHE="$T/c3"
[ $rc -ne 0 ] && grep -q "checksum mismatch" <<<"$out" && ok "a tampered archive is refused" || bad "tampered archive accepted: $out"
cp "$T/arc.good" "$rel/tfpls_${os}_${arch}.tar.gz"

# provenance
run bad TFPLS_TOOL_CACHE="$T/c4"
[ $rc -ne 0 ] && grep -q "provenance" <<<"$out" && [ ! -e "$T/c4/bin" ] && ok "a failed provenance check is refused" || bad "bad provenance accepted: $out"
run none TFPLS_TOOL_CACHE="$T/c5"
[ $rc -eq 0 ] && grep -q "::warning::.*provenance" <<<"$out" && ok "without gh only the checksum is verified, and the log says so" || bad "no-gh case: $out"
run ok TFPLS_TOOL_CACHE="$T/c6" GH_TOKEN=
[ $rc -eq 0 ] && grep -q "::warning::.*provenance" <<<"$out" && ok "without a token only the checksum is verified, and the log says so" || bad "no-token case: $out"

# inputs that are refused
run ok TFPLS_TOOL_CACHE="$T/c7" TFPLS_ARCH=riscv64
[ $rc -ne 0 ] && grep -q "unsupported architecture" <<<"$out" && ok "an unsupported architecture is refused" || bad "arch accepted: $out"
run ok TFPLS_TOOL_CACHE="$T/c7" TFPLS_OS=plan9
[ $rc -ne 0 ] && grep -q "unsupported OS" <<<"$out" && ok "an unsupported OS is refused" || bad "os accepted: $out"
printf 'v9.9\n' > "$T/VERSION.bad"
run ok TFPLS_TOOL_CACHE="$T/c7" TFPLS_VERSION_FILE="$T/VERSION.bad"
[ $rc -ne 0 ] && grep -q "bad tfpls version" <<<"$out" && ok "a malformed version is refused" || bad "bad version accepted: $out"
run ok TFPLS_TOOL_CACHE="$T/c7" TFPLS_RELEASE_BASE="file://$T/nowhere"
[ $rc -ne 0 ] && grep -q "download failed" <<<"$out" && ok "a missing release fails loudly" || bad "missing release: $out"

# dev: built from the source of this checkout
printf 'dev\n' > "$T/VERSION.dev"
run none TFPLS_VERSION_FILE="$T/VERSION.dev" RUNNER_TEMP="$T/rt" HOME="$HOME" PATH="$(dirname "$(command -v go)"):$T/path-bin:/usr/bin:/bin"
[ $rc -eq 0 ] && [ "$("$(pathdir)/tfpls" version)" = dev ] && grep -q "building tfpls from the source" <<<"$out" && ok "dev builds the binary from this checkout" || bad "dev build: $out"

# the files that must stay in step
[ "$(tr -d '[:space:]' < "$here/../actions/bin/VERSION")" = dev ] && ok "actions/bin/VERSION is dev on main" || bad "VERSION is not dev"
[ -x "$inst" ] && ok "install.sh is executable" || bad "install.sh is not executable"
done_tests
