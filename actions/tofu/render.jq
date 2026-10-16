# The warning line above the collapsed plan: what will be destroyed or replaced (input: the output of summarize.jq).
if (.destroyed | length) > 0 then
    "> ⚠️ **\(.destroyed | length) resource(s) will be destroyed or replaced:** "
    + ( [ .destroyed[0:10][] | "`\(.address)`" + (if .kind == "replace" then " (replace)" else "" end) ] | join(", ") )
    + ( if (.destroyed | length) > 10 then ", … \((.destroyed | length) - 10) more" else "" end )
  else "" end
