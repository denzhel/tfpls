# Validates .tfpls.yml (as JSON). Output: an array of {level: "error"|"warning", msg}.
# Errors stop the run; warnings are shown (unknown keys are otherwise silently ignored, which hides typos).

def settings: ["auto_apply","create_workspace","var_file","parallelism","policy_dir","policy_packs","policy_params","approvals","notify_secret","notify_format","notify_mention","notify_template","after","drift_remediate","require_reviewed_plan","plan_lock","lock_timeout","drift_fix"];
def stack_keys: ["name","dir","workspaces","workspaces_file","workspaces_glob","workspace_paths","ignore","watch","triggers","overrides"] + settings;
def top_keys: ["defaults","stacks","overrides"];

def norm: ascii_downcase | gsub("[-_ ]"; "");
def err($path; $m): {level: "error", msg: "\($path): \($m)"};
def warn($path; $m): {level: "warning", msg: "\($path): \($m)"};
def hint($k; $known): ([$known[] | select(norm == ($k | norm))] | first) as $m | if $m then " (did you mean `\($m)`?)" else "" end;
def unknown($obj; $known; $path):
  [ $obj | keys[] | select(. as $k | ($known | index($k)) == null) | . as $k
    | warn($path; "unknown key `\($k)` is ignored\(hint($k; $known))") ];

def strlist($v; $path):
  if ($v | type) != "array" then [err($path; "must be a list of strings")]
  elif ($v | all(type == "string")) then [] else [err($path; "must be a list of strings")] end;

def check_setting($k; $v; $path):
  if $k == "auto_apply" then
    (if ($v | type) == "string" and ($v | IN("never","always","additive_only")) then [] else [err($path; "must be never, always or additive_only")] end)
  elif ($k | IN("create_workspace","require_reviewed_plan","plan_lock","drift_fix")) then
    (if ($v | type) == "boolean" then [] else [err($path; "must be true or false (without quotes)")] end)
  elif $k == "parallelism" then
    (if ($v | type) == "number" and $v >= 1 and $v == ($v | floor) then [] else [err($path; "must be a whole number of 1 or more")] end)
  elif $k == "policy_params" then
    (if ($v | type) != "object" or ($v | to_entries | any(.value | type != "object")) then [err($path; "must map a pack name to its parameters, e.g. `safety: { max_destroys: 3 }`")]
     else [ $v | keys[] | select(test("^[a-z0-9][a-z0-9-]*$") | not) | err($path; "pack names are lowercase letters, digits and `-`, got `\(.)`") ] end)
  elif $k == "approvals" then
    [err($path; "was removed in v0.9.0: the number of approvals comes from the repository's own settings (the pull_request rule of a ruleset on the default branch); delete this key. A repository admin can apply in an emergency with `/tofu apply <workspace> --now`")]
  elif $k == "notify_secret" then
    (if ($v | type) == "string" and ($v | test("^[A-Z][A-Z0-9_]*$")) then [] else [err($path; "must be the NAME of a secret (capital letters, digits and `_`), not its value")] end)
  elif $k == "notify_template" then
    (if ($v | type) == "string" and ($v | test("^[A-Za-z0-9_.][A-Za-z0-9_./-]*$")) and ($v | contains("..") | not) then [] else [err($path; "must be a relative path inside the repository (no leading `/`, no `..`), e.g. .github/tfpls/slack.jq")] end)
  elif $k == "notify_format" then
    (if ($v | type) == "string" and ($v | IN("slack","teams","generic")) then [] else [err($path; "must be slack, teams or generic")] end)
  elif $k == "after" then strlist($v; $path)
  elif $k == "drift_remediate" then
    (if ($v | type) == "string" and ($v | IN("never","additive_only","always")) then [] else [err($path; "must be never, additive_only or always")] end)
  elif $k == "policy_packs" then
    (strlist($v; $path) + (if ($v | type) == "array" then [ $v[] | select(type == "string" and test("^[a-z0-9][a-z0-9-]*$") | not) | err($path; "pack names are lowercase letters, digits and `-`, got `\(.)`") ] else [] end))
  elif $k == "lock_timeout" then
    (if ($v | type) == "string" and ($v | test("^[0-9]+(s|m|h)$")) then [] else [err($path; "must look like 30s, 5m or 1h")] end)
  else
    (if ($v | type) == "string" then [] else [err($path; "must be a string")] end)
  end;

def check_settings($obj; $path):
  [ $obj | to_entries[] | select(.key as $k | settings | index($k)) | check_setting(.key; .value; "\($path).\(.key)") ] | add // [];

def check_overrides($ov; $path):
  if ($ov | type) != "object" then [err($path; "must be a map of workspace pattern to settings")]
  else [ $ov | to_entries[]
         | if (.value | type) != "object" then err("\($path)[\"\(.key)\"]"; "must be a map of settings")
           else (check_settings(.value; "\($path)[\"\(.key)\"]") + unknown(.value; settings; "\($path)[\"\(.key)\"]")) end ] | flatten end;

def check_stack($s; $path):
  if ($s | type) != "object" then [err($path; "must be a map")]
  else
    ( if ($s.name | type) != "string" or ($s.name | test("^[A-Za-z0-9_.-]+$") | not) then [err($path; "needs a `name` of letters, digits, `_`, `.` or `-`")] else [] end )
    + ( if ($s | has("dir")) and ($s.dir | type) != "string" then [err("\($path).dir"; "must be a string")] else [] end )
    + ( [ ($s | has("workspaces")), ($s | has("workspaces_file")), ($s | has("workspaces_glob")) ] as $src
        | if ($src | any | not) then [err($path; "needs one of `workspaces`, `workspaces_file` or `workspaces_glob` to say which workspaces exist")]
          elif ($src | map(select(.)) | length) > 1 then [err($path; "set only one of `workspaces`, `workspaces_file` and `workspaces_glob` (they are three ways to say which workspaces exist)")]
          else [] end )
    + ( if ($s | has("workspaces")) then strlist($s.workspaces; "\($path).workspaces") else [] end )
    + ( [ "workspaces_file","workspaces_glob","var_file","policy_dir" ] | map(. as $k | if ($s | has($k)) and ($s[$k] | type) != "string" then err("\($path).\($k)"; ({"workspaces_file": "must be a path to a file with one workspace name per line", "workspaces_glob": "must be a glob such as \"workspaces/*\"", "var_file": "must be a path such as \"env/{workspace}.tfvars\"", "policy_dir": "must be a directory path such as \"policies\""}[$k])) else empty end) )
    + ( [ "workspace_paths","ignore","watch" ] | map(. as $k | if ($s | has($k)) then strlist($s[$k]; "\($path).\($k)")[] else empty end) )
    + ( if ($s | has("triggers")) then
          (if ($s.triggers | type) != "object" then [err("\($path).triggers"; "must map a workspace pattern to a list of workspace patterns")]
           else [ $s.triggers | to_entries[] | strlist(.value; "\($path).triggers[\"\(.key)\"]")[] ] end)
        else [] end )
    + ( if ($s | has("overrides")) then check_overrides($s.overrides; "\($path).overrides") else [] end )
    + check_settings($s; $path)
    + unknown($s; stack_keys; $path)
  end;

# an empty file or no file at all is valid: the built-in defaults apply (one workspace per directory of workspaces/*)
(. // {}) as $c
| if ($c | type) != "object" then [err("config"; "is not a map; see examples/.tfpls.yml")]
else $c | (
  ( if has("workspaces") then [err("workspaces"; "the old top-level `workspaces` list was removed in v0.6.0: describe each entry as a stack under `stacks:` (name, dir, workspaces: [...]) or drop the file to use the default layout, see docs/migration.md")] else [] end )
  + ( if has("overrides") then check_overrides(.overrides; "overrides") else [] end )
  + ( if has("defaults") then
        (if (.defaults | type) != "object" then [err("defaults"; "must be a map of settings")]
         else check_settings(.defaults; "defaults") + unknown(.defaults; settings; "defaults") end)
      else [] end )
  + ( if has("stacks") then
        (if (.stacks | type) != "array" then [err("stacks"; "must be a list")]
         else ([ .stacks | to_entries[] | check_stack(.value; "stacks[\(.key)]")[] ]
               + ( [ .stacks[] | select(type == "object") | .name ] | group_by(.) | map(select(length > 1 and .[0] != null)) | map(err("stacks"; "the stack name `\(.[0])` is used more than once")) )) end)
      else [] end )
  + unknown(del(.workspaces); top_keys; "config")
) end
