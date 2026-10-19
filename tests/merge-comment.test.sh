#!/usr/bin/env bash
# The apply after a merge reports on the merged pull request: apply.yml finds the PR number and hands it to the tofu action.
# Run: bash tests/merge-comment.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$here/tests/lib.sh"
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
A="$here/.github/workflows/apply.yml"

# wiring of apply.yml
eq "detect exposes the merged PR number" "$(yq -r '.jobs.detect.outputs.pr_number' "$A")" '${{ steps.pr.outputs.pr_number }}'
eq "the apply step hands the PR number to the tofu action" "$(yq -r '.jobs.apply.steps[] | select(.id == "t") | .with.pr_number' "$A")" '${{ needs.detect.outputs.pr_number }}'
eq "the apply job may write the comment" "$(yq -r '.jobs.apply.permissions."pull-requests"' "$A")" "write"
eq "the detect job still only reads" "$(yq -r '.jobs.detect.permissions."pull-requests"' "$A")" "read"

# the lookup script of the detect job, run against a fake gh that applies the real --jq expression to a fixture
yq -r '.jobs.detect.steps[] | select(.id == "pr") | .run' "$A" > "$T/pr.sh"
mkdir -p "$T/bin"
cat > "$T/bin/gh" <<'EOF'
#!/usr/bin/env bash
# gh api <path> --jq <expr>: print the fixture through the expression
expr=""; while [ $# -gt 0 ]; do [ "$1" = "--jq" ] && expr=$2; shift; done
jq -r "$expr" "$FIXTURE"
EOF
chmod +x "$T/bin/gh"
lookup() { # <fixture json>: prints "head_sha|pr_number"
  printf '%s' "$1" > "$T/fixture.json"
  : > "$T/out"
  FIXTURE="$T/fixture.json" PATH="$T/bin:$PATH" GITHUB_OUTPUT="$T/out" GITHUB_REPOSITORY=o/r SHA=abc123 bash "$T/pr.sh" >/dev/null 2>&1
  echo "$(sed -n 's/^head_sha=//p' "$T/out")|$(sed -n 's/^pr_number=//p' "$T/out")"
}
eq "a merged PR gives its head SHA and number" "$(lookup '[{"number":7,"merged_at":"2026-10-19T00:00:00Z","head":{"sha":"deadbeef"}}]')" "deadbeef|7"
eq "an open or unmerged PR is ignored" "$(lookup '[{"number":8,"merged_at":null,"head":{"sha":"cafe"}}]')" "|"
eq "a direct push (no PR) gives neither" "$(lookup '[]')" "|"

done_tests
