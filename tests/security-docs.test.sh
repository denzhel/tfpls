#!/usr/bin/env bash
# docs/security-model.md has the "Automated scanning" section, SECURITY.md and the docs index link to it (the anchor matches the heading),
# and the section answers what was asked: what runs when, why on every pull request and every week, how it works and
# what is AI in it, what it costs (also for private repositories), and what a person still does.
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$here" || exit 1
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

doc=docs/security-model.md
# GitHub's anchor for a heading: lower case, punctuation dropped, spaces become hyphens
slug() { tr '[:upper:]' '[:lower:]' <<<"$1" | sed -E 's/[^a-z0-9 _-]//g; s/ /-/g'; }

heading=$(grep -m1 -E '^## Automated scanning$' "$doc")
[ -n "$heading" ] && ok "$doc has the section '## Automated scanning'" || bad "$doc has no section '## Automated scanning'"
anchor=$(slug "${heading#"## "}")
eq "the anchor of the section" "$anchor" "automated-scanning"
grep -qF "](docs/security-model.md#automated-scanning)" SECURITY.md \
  && ok "SECURITY.md links to $doc#automated-scanning" || bad "SECURITY.md does not link to $doc#automated-scanning"
grep -qF "](security-model.md#automated-scanning)" docs/README.md \
  && ok "the docs index links to the section" || bad "docs/README.md does not link to security-model.md#automated-scanning"

sec=$(awk '/^## Automated scanning$/{f=1; next} /^## /{f=0} f' "$doc")
# one sub-heading per question
for h in "What runs and when" "Why on every pull request and every week" "How it works and what is AI" "What it costs" "What a person still does"; do
  grep -qxF "### $h" <<<"$sec" && ok "the section answers: $h" || bad "the section has no sub-heading '### $h'"
done
# the scanners turned on for this repository, and the AI parts, by name
for s in "CodeQL" "Copilot Autofix" "Secret scanning" "Push protection" "Dependabot alerts" "Dependabot security updates" "Private vulnerability reporting" "AI Scan"; do
  grep -qF "$s" <<<"$sec" && ok "the section names $s" || bad "the section does not name $s"
done
# the claims are sourced: the section cites GitHub's documentation
n=$(grep -oE '\(https://docs\.github\.com/[^)]+\)' <<<"$sec" | sort -u | wc -l | tr -d ' ')
[ "$n" -ge 3 ] && ok "the section cites $n pages of GitHub's documentation" || bad "the section cites $n pages of GitHub's documentation, want at least 3"
done_tests
