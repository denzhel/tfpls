# glob -> anchored regex (gitignore-like): `**/` = any directories (also none), `**` = anything,
# `*` = anything except `/`, `?` = one character except `/`
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
def under($dir): if $dir == "." or $dir == "" then true else (. == $dir or startswith($dir + "/")) end;

# changed files that matter to this stack (its dir + extra watch paths)
def relevant($s; $changed):
  ($s.dir // ".") as $dir
  | [ $changed[] | . as $f
      | select(($f | under($dir)) or any(($s.watch // [])[]; . as $w | $f | under($w)))
      | select(any(($s.ignore // [])[]; . as $g | $f | test($g | globre)) | not) ];

# Which workspaces does a change set touch?
#  - no workspace_paths configured: any relevant change => every workspace (safe default)
#  - workspace_paths ("workspaces/{workspace}/"): files owned by a workspace select only it;
#    any other relevant file is shared => every workspace
def select_changed($s; $changed):
  if $changed == null then $s.wslist
  else
    relevant($s; $changed) as $rel
    | if ($rel | length) == 0 then []
      elif (($s.workspace_paths // []) | length) == 0 then $s.wslist
      else
        ($s.wslist | map(. as $w | {w: $w, p: [($s.workspace_paths)[] | gsub("\\{workspace\\}"; $w)]})) as $own
        | ($rel | map(. as $f | select((any($own[]; .p | any(.[]; . as $p | $f | startswith($p)))) | not))) as $shared
        | if ($shared | length) > 0 then $s.wslist
          else [ $own[] | select(.p as $ps | any($rel[]; . as $f | any($ps[]; . as $p | $f | startswith($p)))) | .w ] end
      end
  end;

# `triggers`: { "<workspace glob>": ["<workspace glob>", ...] } - when a workspace is selected by a
# change, the listed workspaces are selected too (transitively, until nothing new is added).
# Only applies to change-based selection, never to explicit `name` selection, and is not filtered
# by `ignore` (the trigger fires on the selected workspace, not on files).
def expand($s; $sel):
  ($s.wslist | map(select(. != null))) as $all
  | [ ($s.triggers // {}) | to_entries[] | {k: (.key | globre), v: [.value[] | globre]} ] as $rules
  | if ($rules | length) == 0 or ($all | length) == 0 then $sel
    else
      def step: . as $cur
        | ($cur + [ $rules[] | select(.k as $k | any($cur[]; test($k))) | .v[] as $p
                    | $all[] | select(test($p)) ]) | unique;
      def fix: . as $c | step | if . == $c then . else fix end;
      ($sel | unique | fix) as $set
      | [ $all[] | select(. as $w | any($set[]; . == $w)) ]
    end;

def selected($s; $changed):
  select_changed($s; $changed) as $sel
  | if $changed == null or ($sel | length) == 0 then $sel else expand($s; $sel) end;

def entries($defaults; $changed):
  [ .stacks[] as $s
    | selected($s; $changed)[] as $w
    | (($s.overrides // {}) as $ov
       | [ $ov | to_entries[] | select(. as $e | (($w // "") | test($e.key | globre)) or ($s.name | test($e.key | globre))) | .value ] | add // {}) as $o
    | ($defaults + $s + $o) as $m
    | {
        name: ($s.name + "/" + ($w // "")),
        stack: $s.name,
        dir: ($m.dir // "."),
        workspace: ($w // ""),
        auto_apply: ($m.auto_apply // "never"),
        drift_remediate: ($m.drift_remediate // ""),
        after: ($m.after // []),
        owner_paths: (($m.workspace_paths // [($m.dir // ".")]) | map(gsub("\\{workspace\\}"; ($w // ""))) | join("\n")),
        policy_params: (($m.policy_params // {}) | if length == 0 then "" else tojson end),
        notify_secret: ($m.notify_secret // ""),
        notify_format: ($m.notify_format // "slack"),
        notify_mention: ($m.notify_mention // ""),
        notify_template: ($m.notify_template // ""),
        policy_dir: ($m.policy_dir // ""),
        policy_packs: (($m.policy_packs // []) | if type == "array" then join(",") else . end),
        var_file: (($m.var_file // "") | gsub("\\{workspace\\}"; ($w // ""))),
        parallelism: (($m.parallelism // 10) | tostring),
        plan_lock: ((if $m.plan_lock == null then false else $m.plan_lock end) | tostring),  # not `// true`: jq treats false as empty
        drift_fix: ((if $m.drift_fix == null then true else $m.drift_fix end) | tostring),
        lock_timeout: (($m.lock_timeout // "2m") | tostring),
        require_reviewed_plan: (($m.require_reviewed_plan // false) | tostring)
      }
  ];

# overrides that match no workspace and no stack name (probably a typo or a pattern for another layout)
def unmatched:
  ([ .stacks[] | (.wslist // [])[] | . // "" ] + [ .stacks[] | .name ]) as $names
  | [ ((.overrides // {}) | keys[]), (.stacks[] | ((.overrides // {}) | keys[])) ] | unique
  | map(select(. as $k | any($names[]; test($k | globre)) | not));

def main:
# how often a workspace name occurs among ALL configured workspaces (not only the selected ones), so that a label never
# changes with what a pull request touches
([ .stacks[] | (.wslist // [])[] | . // "" ] | group_by(.) | map({key: .[0], value: length}) | from_entries) as $count
| entries($defaults; $changed)
# short display label: the workspace name; the stack name when the workspace is the tofu `default` one or its name is used by
# another stack too
| map(. + {label: (if .workspace == "" then .name elif .workspace == "default" then .stack elif $count[.workspace] > 1 then .name else .workspace end)})
| if ($select | length) == 0 then .
  else map(. as $e | select(any($select[]; . as $p
        | $p == "all" or ($e.name | test($p | globre)) or ($e.workspace | test($p | globre)) or ($e.label | test($p | globre))))) end
# `after`: apply this workspace only once the matching workspaces that are part of THIS run have succeeded.
# Resolve the patterns to labels, order the list so every workspace comes after its predecessors (matrix jobs start in
# list order, so a waiting job never holds a runner a predecessor needs), and reject cycles.
| . as $es
| map(. as $e | . + {preds: [ $es[] | select(.name != $e.name) | select(. as $o | any($e.after[]; . as $p | ($p | globre) as $re | ($o.workspace | test($re)) or ($o.name | test($re)) or ($o.label | test($re)))) | .label ]})
| . as $ps
| (length) as $n
| (reduce range(0; $n + 1) as $_ (map({key: .label, value: 0}) | from_entries;
     . as $lv | reduce $ps[] as $e ($lv; .[$e.label] = (([$e.preds[] | $lv[.] + 1] + [0]) | max)))) as $level
| if ([$level[]] | max // 0) > $n then error("`after` has a cycle between workspaces: " + ([ $ps[] | select(.preds | length > 0) | .label ] | join(", "))) else . end
| ($ps | to_entries | map(.value + {idx: .key})) as $indexed
| ($indexed | sort_by($level[.label], .idx))
| map(. + {wait_for: (.preds | join(","))} | del(.preds, .idx, .after));

if ($ARGS.named.probe // "") != "" then unmatched else main end
