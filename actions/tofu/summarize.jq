# Summarize a `tofu show -json` plan: counts, the header line and the changed addresses. Addresses and actions only, never values.
# Input: the plan JSON.  Output: {header, counts, resources: [{address, action}]}

def action_of($acts):
  if $acts == ["create"] then "create"
  elif $acts == ["update"] then "update"
  elif $acts == ["delete"] then "delete"
  elif ($acts | sort) == ["create","delete"] then "replace"
  else "other" end;

[ (.resource_changes // [])[] | select(.change.actions != ["no-op"] and .change.actions != ["read"]) ] as $changes
| ($changes | map(. + {kind: action_of(.change.actions)})) as $rc
| ($rc | map(select(.kind == "create")) | length) as $creates
| ($rc | map(select(.kind == "update")) | length) as $chg
| ($rc | map(select(.kind == "delete")) | length) as $dels
| ($rc | map(select(.kind == "replace")) | length) as $rep
| ($creates + $rep) as $add
| ($dels + $rep) as $del
| {
    counts: {add: $add, change: $chg, destroy: $del, replace: $rep},
    header: ("+\($add) ~\($chg) -\($del)" + (if $rep > 0 then " (\($rep) replace)" else "" end)),
    resources: [ $rc[] | {address, action: .kind} ]
  }
