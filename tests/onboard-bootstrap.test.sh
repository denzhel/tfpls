#!/usr/bin/env bash
# tools/onboard is a small bootstrap: it downloads the tfpls binary of a release, checks it against SHA256SUMS, caches it and
# runs `tfpls onboard`. Tested against a fabricated release on disk (TFPLS_RELEASE_BASE=file://...): a good checksum runs, a
# wrong one is refused, an unsupported OS or architecture is refused, TFPLS_ONBOARD_BIN works offline, flags pass through,
# the version comes from --ref or TFPLS_REF, and the script is short, bash 3.2 safe and shellcheck clean.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
boot="$here/tools/onboard"
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
t=$(mktemp -d); trap 'rm -rf "$t"' EXIT
cd "$t" || exit 1   # a faulty bootstrap must never write into the repository
unset TFPLS_REF TFPLS_ONBOARD_BIN TFPLS_RELEASE_BASE
export XDG_CACHE_HOME="$t/cache" HOME="$t/home"; mkdir -p "$HOME"
sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1; }

# a fabricated release: the "binary" prints what it was called with and which release it is
mkrelease() { # mkrelease <dir> <label> [<os> <arch> ...]: the four assets for this machine's names, one tarball per os_arch
  local d=$1 label=$2 os arch w="$t/w.$RANDOM"; shift 2; mkdir -p "$d" "$w"
  printf '#!/bin/sh\necho "FAKE %s args: $*"\necho ran >> "%s/ran"\n' "$label" "$t" > "$w/tfpls"; chmod +x "$w/tfpls"
  : > "$d/SHA256SUMS"
  for os in linux darwin; do for arch in amd64 arm64; do
    tar -czf "$d/tfpls_${os}_${arch}.tar.gz" -C "$w" tfpls
    printf '%s  %s\n' "$(sha "$d/tfpls_${os}_${arch}.tar.gz")" "tfpls_${os}_${arch}.tar.gz" >> "$d/SHA256SUMS"
  done; done
}
rel="$t/rel"; mkrelease "$rel/latest/download" latest; mkrelease "$rel/download/v0.9.9" v0.9.9
export TFPLS_RELEASE_BASE="file://$rel"
# a fake uname, to test the other machines
mkdir -p "$t/fake"; printf '#!/bin/sh\ncase "$1" in -s) echo "$FAKE_S";; -m) echo "$FAKE_M";; esac\n' > "$t/fake/uname"; chmod +x "$t/fake/uname"
mkdir -p "$t/fakegh"
run() { local o rc; o=$("$@" </dev/null 2>&1); rc=$?; printf '%s\n' "$o" | sed '/^tfpls: build provenance/d'; return $rc; }   # the provenance line depends on gh being installed
printf '#!/bin/sh\necho "$*" >> "%s/gh.log"\n[ "${FAKE_GH:-ok}" = ok ]\n' "$t" > "$t/fakegh/gh"; chmod +x "$t/fakegh/gh"

# ---- the default: the latest release
out=$(run bash "$boot" --dry-run "$t/x"); rc=$?
eq "the latest release runs tfpls onboard with the flags" "$out" "FAKE latest args: onboard --dry-run $t/x"
eq "exit code 0" "$rc" 0
[ -x "$XDG_CACHE_HOME/tfpls/latest/tfpls" ] && ok "the binary is cached under the cache directory" || bad "not cached"
out=$(PATH="$t/fake:$PATH" FAKE_S=Linux FAKE_M=aarch64 run bash "$boot" --yes); eq "linux aarch64 is arm64 and works" "$out" "FAKE latest args: onboard --yes"
out=$(PATH="$t/fake:$PATH" FAKE_S=Darwin FAKE_M=x86_64 run bash "$boot" --yes); eq "darwin x86_64 is amd64 and works" "$out" "FAKE latest args: onboard --yes"

# ---- the provenance check is informational: shown with gh, never required
rm -rf "$XDG_CACHE_HOME"; out=$(PATH="$t/fakegh:$PATH" bash "$boot" --yes </dev/null 2>&1)
grep -q 'build provenance verified' <<<"$out" && grep -q 'attestation verify .*--repo denzhel/tfpls' "$t/gh.log" && ok "with gh the provenance is checked and reported" || bad "provenance: $out"
rm -rf "$XDG_CACHE_HOME"; out=$(PATH="$t/fakegh:$PATH" FAKE_GH=no bash "$boot" --yes </dev/null 2>&1); rc=$?
[ "$rc" -eq 0 ] && grep -q 'build provenance not checked' <<<"$out" && grep -q 'FAKE latest' <<<"$out" && ok "a failing provenance check does not block (the checksum is what is required)" || bad "provenance fail: rc=$rc $out"

# ---- refused: a wrong checksum, a tampered archive, an unsupported machine
rm -rf "$XDG_CACHE_HOME" "$t/ran"; bad_rel="$t/bad"; cp -R "$rel" "$bad_rel"
sed 's/^[0-9a-f]\{8\}/00000000/' "$bad_rel/latest/download/SHA256SUMS" > "$bad_rel/x" && mv "$bad_rel/x" "$bad_rel/latest/download/SHA256SUMS"
out=$(TFPLS_RELEASE_BASE="file://$bad_rel" run bash "$boot" --yes); rc=$?
[ "$rc" -ne 0 ] && grep -qi 'checksum' <<<"$out" && ok "a checksum mismatch is refused with a message" || bad "mismatch: rc=$rc $out"
[ ! -e "$t/ran" ] && [ ! -e "$XDG_CACHE_HOME/tfpls/latest/tfpls" ] && ok "a refused binary is neither run nor cached" || bad "refused binary ran or was cached"
cp -R "$rel" "$t/bad2"; printf 'tampered' >> "$t/bad2/latest/download/tfpls_linux_amd64.tar.gz"; printf 'tampered' >> "$t/bad2/latest/download/tfpls_darwin_arm64.tar.gz"
out=$(TFPLS_RELEASE_BASE="file://$t/bad2" run bash "$boot" --yes); rc=$?
[ "$rc" -ne 0 ] && grep -qi 'checksum' <<<"$out" && [ ! -e "$t/ran" ] && ok "a tampered archive is refused" || bad "tampered: rc=$rc $out"
out=$(PATH="$t/fake:$PATH" FAKE_S=Linux FAKE_M=riscv64 run bash "$boot" --yes); rc=$?
[ "$rc" -ne 0 ] && grep -qi 'unsupported' <<<"$out" && ok "an unsupported architecture is refused" || bad "arch: rc=$rc $out"
out=$(PATH="$t/fake:$PATH" FAKE_S=FreeBSD FAKE_M=x86_64 run bash "$boot" --yes); rc=$?
[ "$rc" -ne 0 ] && grep -qi 'unsupported' <<<"$out" && ok "an unsupported OS is refused" || bad "os: rc=$rc $out"
out=$(TFPLS_RELEASE_BASE="file://$t/nowhere" run bash "$boot" --yes); rc=$?
[ "$rc" -ne 0 ] && grep -qi 'cannot download' <<<"$out" && ok "a failed download is an error, not a silent run" || bad "download: rc=$rc $out"

# ---- the version: --ref, TFPLS_REF, a tag that has no release
rm -rf "$XDG_CACHE_HOME"
out=$(run bash "$boot" --ref v0.9.9 --yes); eq "--ref of a release tag downloads that release and passes --ref on" "$out" "FAKE v0.9.9 args: onboard --ref v0.9.9 --yes"
out=$(TFPLS_REF=v0.9.9 run bash "$boot" --yes); eq "TFPLS_REF picks the release" "$out" "FAKE v0.9.9 args: onboard --yes"
[ -x "$XDG_CACHE_HOME/tfpls/v0.9.9/tfpls" ] && ok "a version is cached under its own directory" || bad "version cache"
out=$(TFPLS_RELEASE_BASE="file://$t/nowhere" run bash "$boot" --ref v0.9.9 --yes); eq "a cached version needs no network" "$out" "FAKE v0.9.9 args: onboard --ref v0.9.9 --yes"
out=$(run bash "$boot" --ref v0 --yes); eq "--ref v0 (the moving tag of the caller) is not a release: the latest runs" "$out" "FAKE latest args: onboard --ref v0 --yes"
out=$(run bash "$boot" --ref v0.0.1 --yes); grep -q 'FAKE latest args: onboard --ref v0.0.1 --yes' <<<"$out" && grep -qi 'no release' <<<"$out" && ok "--ref of a tag without release assets falls back to the latest, and says so" || bad "fallback: $out"
out=$(TFPLS_REF=v0.0.1 run bash "$boot" --yes); rc=$?; [ "$rc" -ne 0 ] && ok "TFPLS_REF of a missing release is an error (no silent fallback)" || bad "TFPLS_REF fallback: $out"

# ---- offline: TFPLS_ONBOARD_BIN skips the download
printf '#!/bin/sh\necho "LOCAL $*"\n' > "$t/local"; chmod +x "$t/local"
out=$(TFPLS_ONBOARD_BIN="$t/local" TFPLS_RELEASE_BASE="file://$t/nowhere" run bash "$boot" --dry-run a b); eq "TFPLS_ONBOARD_BIN runs that binary, nothing is downloaded" "$out" "LOCAL onboard --dry-run a b"

# ---- under curl | bash: the script comes from stdin, the arguments after -s --
out=$(cat "$boot" | bash -s -- --dry-run "$t/x" 2>&1 | sed '/^tfpls: build provenance/d'); eq "cat tools/onboard | bash -s -- flags passes the flags" "$out" "FAKE latest args: onboard --dry-run $t/x"
out=$(printf 'typed\n' | TFPLS_ONBOARD_BIN="$t/local" bash "$boot" 2>&1); eq "the bootstrap does not read stdin itself" "$out" "LOCAL onboard"

# ---- the real binary through the bootstrap (this machine's os and arch)
if command -v go >/dev/null 2>&1; then
  os=$(uname -s | tr 'A-Z' 'a-z'); arch=$(uname -m); case $arch in x86_64) arch=amd64;; aarch64) arch=arm64;; esac
  real="$t/real/latest/download"; mkdir -p "$real" "$t/rb"
  (cd "$here" && go build -o "$t/rb/tfpls" ./cmd/tfpls) && tar -czf "$real/tfpls_${os}_${arch}.tar.gz" -C "$t/rb" tfpls \
    && printf '%s  %s\n' "$(sha "$real/tfpls_${os}_${arch}.tar.gz")" "tfpls_${os}_${arch}.tar.gz" > "$real/SHA256SUMS"
  mkdir -p "$t/proj/workspaces/dev"
  out=$(NO_COLOR=1 TFPLS_RELEASE_BASE="file://$t/real" XDG_CACHE_HOME="$t/cache2" run bash "$boot" --dry-run --yes --no-jokes "$t/proj")
  grep -q '1 workspace(s) in workspaces/: dev' <<<"$out" && grep -q 'tfpls.*onboarding' <<<"$out" && [ ! -e "$t/proj/.github" ] && ok "the downloaded real binary onboards a repository (dry run)" || bad "real binary: $out"
fi

# ---- the script itself: short, bash 3.2 safe, shellcheck clean
n=$(wc -l < "$boot" | tr -d ' ')
[ "$n" -le 70 ] && ok "the bootstrap is $n lines (at most 70)" || bad "the bootstrap is $n lines"
if [ -x /bin/bash ] && [ "$(/bin/bash -c 'echo "${BASH_VERSINFO[0]}"')" = 3 ]; then
  /bin/bash -n "$boot" && ok "bash 3.2 parses the bootstrap" || bad "bash 3.2 syntax"
  out=$(TFPLS_ONBOARD_BIN="$t/local" /bin/bash "$boot" --yes 2>&1); eq "bash 3.2 runs the bootstrap" "$out" "LOCAL onboard --yes"
  out=$(/bin/bash "$boot" --ref v0.9.9 --yes 2>&1); eq "bash 3.2 downloads and runs a release" "$out" "FAKE v0.9.9 args: onboard --ref v0.9.9 --yes"
fi
[ "$(head -1 "$boot")" = '#!/usr/bin/env bash' ] && tail -1 "$boot" | grep -q '^main "\$@"$' && ok "everything is in functions and main runs on the last line (a cut download runs nothing)" || bad "main on the last line"
if command -v shellcheck >/dev/null 2>&1; then shellcheck -S warning "$boot" && ok "shellcheck: the bootstrap is clean" || bad "shellcheck"; fi

done_tests
