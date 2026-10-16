#!/usr/bin/env bash
# detect-version.sh [dir]  -> prints the OpenTofu version a repository asks for, or nothing.
# Order: .opentofu-version, .tofu-version, .terraform-version (one version, optional leading v, # comments allowed),
# then an EXACT required_version in a *.tf file of the directory ("1.11.4" or "= 1.11.4"; ranges like ">= 1.6" are ignored).
set -uo pipefail
dir=${1:-.}
for f in .opentofu-version .tofu-version .terraform-version; do
  if [ -f "$dir/$f" ]; then
    v=$(sed -E 's/#.*//; s/^[[:space:]]+//; s/[[:space:]]+$//' "$dir/$f" | grep -v '^$' | head -1)
    v=${v#v}
    if [[ "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then echo "$v"; exit 0; fi
  fi
done
for f in "$dir"/*.tf; do
  [ -f "$f" ] || continue
  v=$(sed -nE 's/^[[:space:]]*required_version[[:space:]]*=[[:space:]]*"[[:space:]]*=?[[:space:]]*v?([0-9]+\.[0-9]+\.[0-9]+)[[:space:]]*".*$/\1/p' "$f" | head -1)
  [ -n "$v" ] && { echo "$v"; exit 0; }
done
exit 0
