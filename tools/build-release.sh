#!/usr/bin/env bash
# Build the release assets of the tfpls binary: tools/build-release.sh <tag> <outdir>
#   <outdir>/tfpls_<os>_<arch>.tar.gz   os linux|darwin, arch amd64|arm64; the tarball holds the single file "tfpls"
#   <outdir>/SHA256SUMS                 sha256 of the tarballs, "<hash>  <name>" (check with: sha256sum -c SHA256SUMS)
#   <outdir>/RELEASE_NOTES.md           the CHANGELOG.md section of the tag, else a one-line note (not a release asset)
# The names do not contain the version, so releases/latest/download/<name> works. Used by .github/workflows/release.yml.
# TFPLS_CHANGELOG overrides the changelog path (tests).
set -uo pipefail

usage() { echo "usage: tools/build-release.sh <tag, e.g. v0.5.0 or v0.5.0-rc.1> <outdir>" >&2; exit 2; }
[ $# -eq 2 ] || usage
tag=$1; out=$2
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || { echo "build-release: '$tag' is not a version tag (vX.Y.Z or vX.Y.Z-rc.N)" >&2; exit 2; }
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
changelog=${TFPLS_CHANGELOG:-$root/CHANGELOG.md}

mkdir -p "$out" || exit 1
out=$(cd "$out" && pwd)
work=$(mktemp -d) || exit 1
trap 'rm -rf "$work"' EXIT

for os in linux darwin; do
  for arch in amd64 arm64; do
    name="tfpls_${os}_${arch}"
    mkdir -p "$work/$name" || exit 1
    ( cd "$root" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
        -ldflags "-s -w -X main.version=$tag" -o "$work/$name/tfpls" ./cmd/tfpls ) || { echo "build-release: build of $name failed" >&2; exit 1; }
    chmod 0755 "$work/$name/tfpls"
    tar -C "$work/$name" -cf - tfpls | gzip -n -9 > "$out/$name.tar.gz" || exit 1
  done
done

( cd "$out" || exit 1
  : > SHA256SUMS
  for f in tfpls_*.tar.gz; do
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$f" >> SHA256SUMS; else shasum -a 256 "$f" >> SHA256SUMS; fi
  done ) || exit 1

# the CHANGELOG section "## <tag>" up to the next "## ", without blank lines at the ends
notes=""
if [ -f "$changelog" ]; then
  notes=$(awk -v h="## $tag" '
    $0 == h { on = 1; next }
    /^## / { on = 0 }
    on { lines[++n] = $0 }
    END {
      s = 1; while (s <= n && lines[s] == "") s++
      e = n; while (e >= s && lines[e] == "") e--
      for (i = s; i <= e; i++) print lines[i]
    }' "$changelog")
fi
if [ -n "$notes" ]; then printf '%s\n' "$notes" > "$out/RELEASE_NOTES.md"
else printf 'tfpls %s (no CHANGELOG section for this version).\n' "$tag" > "$out/RELEASE_NOTES.md"; fi
echo "build-release: $tag built into $out"
