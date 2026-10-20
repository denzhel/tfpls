#!/usr/bin/env bash
# tools/build-release.sh: the four release binaries, their tarballs, SHA256SUMS and the release notes. Builds into a temp dir with
# a fake tag; nothing is published. Run: bash tests/build-release.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
cd "$here" || exit 1
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
S=tools/build-release.sh
tag=v9.9.9-test.1

if ! command -v go >/dev/null 2>&1; then echo "skip: go is not installed"; exit 0; fi
[ -f "$S" ] && ok "tools/build-release.sh exists" || { bad "tools/build-release.sh is missing"; done_tests; exit; }

out=$(bash "$S" "$tag" "$T/dist" 2>&1); rc=$?
[ $rc -eq 0 ] && ok "the build succeeds" || { bad "build failed ($rc): $out"; done_tests; exit; }

want=$(printf 'SHA256SUMS\ntfpls_darwin_amd64.tar.gz\ntfpls_darwin_arm64.tar.gz\ntfpls_linux_amd64.tar.gz\ntfpls_linux_arm64.tar.gz\n')
eq "the assets are the four tarballs and SHA256SUMS, with version-independent names" \
  "$(find "$T/dist" -type f ! -name RELEASE_NOTES.md -exec basename {} \; | sort)" "$want"
for t in "$T"/dist/tfpls_*.tar.gz; do
  eq "$(basename "$t") holds the single file tfpls" "$(tar -tzf "$t")" "tfpls"
done

# SHA256SUMS: "<hash>  <name>", one line per tarball, valid for the files
sum() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
chk() { if command -v sha256sum >/dev/null 2>&1; then sha256sum -c SHA256SUMS; else shasum -a 256 -c SHA256SUMS; fi; }
eq "SHA256SUMS has one line per tarball" "$(wc -l < "$T/dist/SHA256SUMS" | tr -d ' ')" 4
re='^[0-9a-f]{64}  tfpls_(linux|darwin)_(amd64|arm64)\.tar\.gz$'
if grep -qE "$re" "$T/dist/SHA256SUMS" && ! grep -qvE "$re" "$T/dist/SHA256SUMS"; then ok "SHA256SUMS lines are '<hash>  <name>'"; else bad "SHA256SUMS format"; fi
if ( cd "$T/dist" && chk >/dev/null 2>&1 ); then ok "SHA256SUMS verifies the tarballs"; else bad "SHA256SUMS does not verify"; fi
eq "the hash is the sha256 of the tarball" "$(grep ' tfpls_linux_amd64.tar.gz$' "$T/dist/SHA256SUMS" | cut -d' ' -f1)" "$(sum "$T/dist/tfpls_linux_amd64.tar.gz" | cut -d' ' -f1)"

# the version stamp: the binary of this host prints the tag
os=$(uname -s | tr '[:upper:]' '[:lower:]'); case "$(uname -m)" in x86_64|amd64) arch=amd64;; *) arch=arm64;; esac
mkdir "$T/x" && tar -xzf "$T/dist/tfpls_${os}_${arch}.tar.gz" -C "$T/x"
eq "tfpls version of the host binary prints the tag" "$("$T/x/tfpls" version 2>&1)" "$tag"

# a changed byte fails the verification
cp -R "$T/dist" "$T/bad"
printf 'x' >> "$T/bad/tfpls_linux_arm64.tar.gz"
if ( cd "$T/bad" && chk >/dev/null 2>&1 ); then bad "a changed tarball still verifies"; else ok "a changed byte fails verification"; fi

# release notes: the CHANGELOG section of the version, else a one-line note
printf '# Changelog\n\n## Unreleased\n\n- next\n\n## v1.2.3\n\n- first line\n- second line\n\n## v1.2.2\n\n- old\n' > "$T/CHANGELOG.md"
TFPLS_CHANGELOG="$T/CHANGELOG.md" bash "$S" v1.2.3 "$T/n1" >/dev/null 2>&1
eq "notes are the CHANGELOG section of the version" "$(cat "$T/n1/RELEASE_NOTES.md" 2>/dev/null)" "$(printf -- '- first line\n- second line')"
TFPLS_CHANGELOG="$T/CHANGELOG.md" bash "$S" v1.2.4-rc.1 "$T/n2" >/dev/null 2>&1
eq "no section: a one-line note" "$(wc -l < "$T/n2/RELEASE_NOTES.md" | tr -d ' ')" 1
if grep -q 'v1.2.4-rc.1' "$T/n2/RELEASE_NOTES.md"; then ok "the one-line note names the version"; else bad "the fallback note does not name the version"; fi

# bad input
if bash "$S" "1.2.3" "$T/n3" >/dev/null 2>&1; then bad "a tag without v is accepted"; else ok "a tag without the v prefix is refused"; fi
if bash "$S" >/dev/null 2>&1; then bad "no arguments accepted"; else ok "no arguments are refused"; fi

done_tests
