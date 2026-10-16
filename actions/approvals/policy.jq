# Which approval rules apply to what a pull request wants to apply?
# Input : the entries detect-workspaces made from the PULL REQUEST's config (an array).
# --argjson base : the config of the DEFAULT BRANCH as JSON ({} when the file does not exist).
# Output: { rules, entries, owner_entries }
#   rules         one object per (head entry x matching base stack): approvals_* and owner_paths of the base config
#   entries       the head entries with policy_dir, policy_packs, policy_params and notify_secret taken from the base rule
#   owner_entries rules + head entries (CODEOWNERS paths from both)
# A stack is matched by (normalized dir, workspace), never by its name: a pull request cannot escape a protected
# directory by giving its stack another name. Nothing matches -> the base defaults (and the base top-level overrides) apply.

def globre:
  "^" + (
    gsub("(?<c>[.+^${}()|\\[\\]\\\\])"; "\\" + .c)
    | gsub("\\*\\*/"; "\u0001")
    | gsub("\\*\\*"; "\u0002")
    | gsub("\\*"; "[^/]*")
    | gsub("\\?"; "[^/]")
    | gsub("\u0001"; "(.*/)?")
    | gsub("\u0002"; ".*")
  ) + "$";
def normdir:
  (. // "." | tostring | split("/") | map(select(. != "" and . != "."))
   | reduce .[] as $p ([]; if $p == ".." then .[:-1] else . + [$p] end)) as $parts
  | if ($parts | length) == 0 then "." else ($parts | join("/")) end;

($base // {}) as $b
| ($b.defaults // {}) as $defaults
| ($b.overrides // {}) as $top
| (if ($b.stacks // null) != null
   then ($b.stacks | map(. + {overrides: ($top + (.overrides // {}))}))
   else [ { name: "terraform", dir: ".", workspaces_glob: "workspaces/*", workspace_paths: ["workspaces/{workspace}/"], overrides: $top } ] end) as $stacks
| def rule($t; $s):
    ($t.workspace // "") as $w
    | ((($s.overrides // {}) | to_entries | map(select(. as $e | ($w | test($e.key | globre)) or (($s.name // "") | test($e.key | globre))) | .value) | add) // {}) as $o
    | ($defaults + $s + $o) as $m
    | {
        name: ($t.label // $t.name),
        owner_paths: (($m.workspace_paths // [($m.dir // ".")]) | map(gsub("\\{workspace\\}"; $w)) | join("\n")),
        policy_dir: ($m.policy_dir // ""),
        policy_packs: (($m.policy_packs // []) | if type == "array" then join(",") else . end),
        policy_params: (($m.policy_params // {}) | if length == 0 then "" else tojson end),
        notify_secret: ($m.notify_secret // "")
      };
  def rules_for($t):
    ($t.dir | normdir) as $d
    | ($t.workspace // "") as $w
    | [ $stacks[] | select((.dir | normdir) == $d)
        | select((.workspaces // null) == null or ((.workspaces | index($w)) != null)) ] as $c
    | if ($c | length) == 0 then [ rule($t; {dir: $t.dir, overrides: $top}) ] else [ $c[] | rule($t; .) ] end;

  . as $heads
| [ $heads[] | . as $t | {t: $t, r: rules_for($t)} ] as $pairs
| {
    rules: [ $pairs[] | .r[] ],
    entries: [ $pairs[] | .t + (.r[0] | {policy_dir, policy_packs, policy_params, notify_secret}) ],
    owner_entries: ([ $pairs[] | .r[] ] + $heads)
  }
