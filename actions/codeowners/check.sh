#!/usr/bin/env bash
# CODEOWNERS-driven approvals for /tofu apply.
#   check.sh <CODEOWNERS file> <reviews.json> <head-sha> <path>...
# For every path (a workspace directory) the owners come from the last CODEOWNERS rule that matches it. The path is
# satisfied when at least one owner has an APPROVED review of the current head commit (latest review per person wins).
# Owners may be users (@alice) or teams (@org/team: members are read with `gh api`, or from $TFPLS_TEAMS_FILE in tests,
# lines "@org/team alice bob"). A path without any owner has no requirement.
# Exit: 0 satisfied, 1 missing approvals (explained on stdout), 2 usage.
set -uo pipefail
co=${1:-}; reviews=${2:-}; sha=${3:-}; shift 3 2>/dev/null || true
[ -f "$co" ] && [ -f "$reviews" ] && [ -n "$sha" ] && [ "$#" -gt 0 ] || { echo "usage: check.sh <CODEOWNERS> <reviews.json> <sha> <path>..." >&2; exit 2; }

approvers=$(jq -r --arg sha "$sha" '[.[] | select(.state == "APPROVED" or .state == "CHANGES_REQUESTED" or .state == "DISMISSED")]
  | group_by(.user.login) | map(sort_by(.submitted_at) | last) | .[] | select(.state == "APPROVED" and .commit_id == $sha) | .user.login | ascii_downcase' "$reviews")
is_approver() { grep -qxF "$(printf '%s' "$1" | tr 'A-Z' 'a-z')" <<<"$approvers"; }

team_members() { # @org/team
  if [ -n "${TFPLS_TEAMS_FILE:-}" ]; then awk -v t="$1" '$1 == t { for (i = 2; i <= NF; i++) print $i }' "$TFPLS_TEAMS_FILE"; return; fi
  local org=${1#@}; local slug=${org#*/}; org=${org%%/*}
  gh api --paginate "orgs/$org/teams/$slug/members" --jq '.[].login' 2>/dev/null
}

# last matching rule for a path -> owners (space separated)
owners_of() {
  local path=$1
  jq -Rr --arg p "$path/x" '
    def re($pat):
      ( if ($pat | startswith("/")) then $pat[1:] else $pat end ) as $body
      | ( $body
          | gsub("(?<c>[.+^${}()|\\[\\]\\\\])"; "\\" + .c)
          | gsub("\\*\\*/"; "\u0001") | gsub("\\*\\*"; "\u0002") | gsub("\\*"; "[^/]*") | gsub("\\?"; "[^/]")
          | gsub("\u0001"; "(.*/)?") | gsub("\u0002"; ".*") ) as $g
      | ( if ($pat | startswith("/")) then "^" elif ($pat | test("/.")) then "^" else "^(.*/)?" end ) + $g
      | if ($pat | endswith("/")) then . + ".*$" else . + "(/.*)?$" end;
    select(test("^\\s*[^#\\s]")) | sub("#.*$"; "") | split("\\s+"; null) | map(select(length > 0))
    | select(length >= 2) | {pat: .[0], owners: .[1:]} | . as $r | select($p | test(re($r.pat))) | $r.owners | join(" ")' "$co" | tail -1
}

missing=""
for path in "$@"; do
  o=$(owners_of "${path%/}")
  [ -n "$o" ] || continue
  ok=0
  for who in $o; do
    case "$who" in
      @*/*) for m in $(team_members "$who"); do is_approver "$m" && { ok=1; break; }; done;;
      @*)   is_approver "${who#@}" && ok=1;;
      *)    ;;   # e-mail owners cannot be matched to reviews
    esac
    [ "$ok" -eq 1 ] && break
  done
  [ "$ok" -eq 1 ] || missing+="- \`$path\` needs an approval of the current commit from one of: $o"$'\n'
done
if [ -n "$missing" ]; then printf 'CODEOWNERS approval is missing:\n%s' "$missing"; exit 1; fi
echo "code owners have approved"
