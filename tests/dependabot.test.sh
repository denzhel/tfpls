#!/usr/bin/env bash
# .github/dependabot.yml: version updates for every action tfpls pins (the workflows and the composite actions), weekly at a
# fixed time, minor and patch grouped, each major on its own, a cooldown, the label and the commit prefix; the pins keep the
# form Dependabot rewrites (a full SHA with the version as a comment on the same line); nothing merges a pull request by itself.
# Run: bash tests/dependabot.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
cd "$here" || exit 1
D=.github/dependabot.yml
u='.updates[] | select(."package-ecosystem" == "github-actions")'
q() { yq -r "$1" "$D" 2>/dev/null; }

if yq '.' "$D" >/dev/null 2>&1 && [ -s "$D" ]; then ok "dependabot.yml exists and parses"; else bad "dependabot.yml is missing or not valid YAML"; fi
eq "configuration version 2" "$(q '.version')" 2
eq "one github-actions entry" "$(q "[$u] | length")" 1
eq "it reads the workflows (/) and the composite actions (/actions/*)" "$(q "[$u | .directories[]] | sort | join(\",\")")" "/,/actions/*"
# /actions/* matches one level: every composite action must sit directly below actions/
n=$(ls actions/*/action.yml 2>/dev/null | wc -l | tr -d ' ')
deep=$(find actions -mindepth 3 -name 'action.y*ml' 2>/dev/null | wc -l | tr -d ' ')
[ "$n" -gt 0 ] && [ "$deep" = 0 ] && ok "all $n composite actions are directly below actions/, so /actions/* covers them" || bad "composite actions: $n at one level, $deep deeper"
eq "weekly at a fixed time in UTC" "$(q "$u | .schedule | [.interval, .day, .time, .timezone] | join(\" \")")" "weekly monday 05:00 Etc/UTC"
cd_days=$(q "$u | .cooldown.\"default-days\"")
{ [[ "$cd_days" =~ ^[0-9]+$ ]] && [ "$cd_days" -ge 3 ] && [ "$cd_days" -le 90 ]; } && ok "a cooldown of $cd_days days (at least GitHub's default of 3, at most its limit of 90)" || bad "cooldown default-days: '$cd_days'"
eq "only default-days (github-actions has no semver-specific cooldown)" "$(q "$u | .cooldown | keys | join(\",\")")" "default-days"
eq "groups: minor and patch together; each major on its own, one pull request per action across the directories" \
  "$(q "$u | .groups | to_entries | map(.key + \"=\" + (.value.patterns | join(\"+\")) + \"/\" + (.value.\"update-types\" | join(\"+\")) + \"/\" + (.value.\"group-by\" // \"-\")) | sort | join(\" \")")" \
  "actions-major=*/major/dependency-name actions-minor-patch=*/minor+patch/-"
eq "the groups are for version updates" "$(q "[$u | .groups[] | .\"applies-to\"] | unique | join(\",\")")" "version-updates"
lim=$(q "$u | .\"open-pull-requests-limit\"")
[[ "$lim" =~ ^[0-9]+$ ]] && [ "$lim" -ge 2 ] && ok "open-pull-requests-limit $lim (the group plus majors)" || bad "open-pull-requests-limit: '$lim'"
eq "the label" "$(q "$u | .labels | join(\",\")")" "dependencies"
eq "the commit prefix" "$(q "$u | .\"commit-message\".prefix")" "deps"

# the pins are in the form Dependabot keeps up to date: a full SHA and the version as a comment on the same line
bad_pins=$(grep -hnE '^[[:space:]]*(-[[:space:]]*)?uses:[[:space:]]*[^.[:space:]][^[:space:]]*@' .github/workflows/*.yml actions/*/action.yml \
  | grep -v 'denzhel/tfpls/' | grep -vE '@[0-9a-f]{40} # v[0-9]+\.[0-9]+\.[0-9]+[[:space:]]*$' || true)
[ -z "$bad_pins" ] && ok "every third-party pin is a SHA with a same-line # vX.Y.Z comment" || bad "pins Dependabot would not keep in this form: $bad_pins"

# no auto-merge: nothing in the workflows merges a pull request or turns auto-merge on
eq "no workflow merges a pull request or enables auto-merge" "$(grep -lE 'gh pr merge|enablePullRequestAutoMerge|auto-merge' .github/workflows/*.yml 2>/dev/null | wc -l | tr -d ' ')" 0

done_tests
