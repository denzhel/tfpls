#!/usr/bin/env bash
# The static rules of the issue agent (claude-elaborate.yml, claude-implement.yml): cost controls, who can start it, what its tools
# can do, and the documents it follows. These are the guards against a later change that loosens them. Run: bash tests/agent-workflows.test.sh
set -uo pipefail
here=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
. "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
W="$here/.github/workflows"
E="$W/claude-elaborate.yml"; I="$W/claude-implement.yml"
y() { yq -r "$2" "$1"; }
args() { y "$1" '.jobs.agent.steps[] | select(.id == "claude") | .with.claude_args'; }
prompt() { y "$1" '.jobs.agent.steps[] | select(.id == "claude") | .with.prompt'; }

for f in "$E" "$I"; do
  n=$(basename "$f")
  # ---- cost: no runner for events that do not match, a gate before the model, hard limits
  eq "$n: the gate job has a free job-level filter" "$(y "$f" '.jobs.gate.if | length > 0')" "true"
  y "$f" '.jobs.gate.if' | grep -q "TFPLS_AGENT_ENABLED != 'false'" && ok "$n: the kill switch is in the free filter" || bad "$n: kill switch missing"
  y "$f" '.jobs.gate.if' | grep -q "sender.type != 'Bot'" && ok "$n: bots never start it (no loop)" || bad "$n: bot filter missing"
  y "$f" '.jobs.gate.if' | grep -q "pull_request == null" && ok "$n: pull requests are ignored" || bad "$n: PR filter missing"
  eq "$n: the model job waits for the gate" "$(y "$f" '.jobs.agent.needs')" "gate"
  y "$f" '.jobs.agent.if' | grep -q "^needs.gate.outputs.run == 'true'" && ok "$n: the model job only runs when the gate says so" || bad "$n: agent condition"
  eq "$n: the gate is the command that is tested offline, after the binary is installed" "$(y "$f" '.jobs.gate.steps[] | select(.id == "g") | .run' | grep -c '^tfpls gate ')|$(y "$f" '[.jobs.gate.steps[1].uses, .jobs.gate.steps[2].id] | join(",")')" "1|./actions/bin,g"
  eq "$n: the report is the command that is tested offline" "$(y "$f" '.jobs.agent.steps[] | select(.name == "Report what the run used") | .run')" "tfpls agent-report"
  [ "$(y "$f" '.jobs.gate.timeout-minutes')" -le 5 ] && ok "$n: the gate job is short" || bad "$n: gate timeout"
  # the gate job is reached by more people than the agent job: it gets only what it needs (no OIDC token, no write to the code)
  eq "$n: the gate job's own permissions: read the code, write issues, read runs (the daily cap)" \
    "$(y "$f" '.jobs.gate.permissions | to_entries | map(.key + ":" + .value) | sort | join(",")')" "actions:read,contents:read,issues:write"
  eq "$n: the gate's checkout keeps no token" "$(y "$f" '.jobs.gate.steps[0].with."persist-credentials"')" "false"
  # never silent: the gate's status comment is posted, and a filled-in issue gets the gate's label
  y "$f" '.jobs.gate.steps[] | select(.if == "steps.g.outputs.comment_file != '"''"'") | .run' | grep -q 'gh issue comment' && ok "$n: a step posts the gate's status comment" || bad "$n: no status comment step"
  y "$f" '.jobs.gate.steps[] | select(.if == "steps.g.outputs.label != '"''"'") | .run' | grep -q 'issues/$ISSUE/labels' && ok "$n: a step adds the label the gate decided" || bad "$n: no label step"
  y "$f" '.jobs.agent.steps[] | select(.name == "Stop with a message when the Claude token is missing") | .run' | grep -q '\*\*Next:\*\*' && ok "$n: even the missing-token comment says what happens next" || bad "$n: token comment without Next"
  eq "$n: one run at a time per issue, never cancelling a running one" "$(y "$f" '.concurrency.group | test("^claude-issue-")')|$(y "$f" '.concurrency."cancel-in-progress"')" "true|false"
  grep -q 'pull_request_target' "$f" && bad "$n: uses pull_request_target" || ok "$n: no pull_request_target"
  a=$(args "$f")
  turns=$(grep -oE -- '--max-turns [0-9]+' <<<"$a" | grep -oE '[0-9]+')
  [ -n "$turns" ] && ok "$n: a turn limit ($turns)" || bad "$n: no --max-turns"
  grep -qE -- "--model \\\$\\{\\{ vars\\.TFPLS_AGENT_MODEL_(ELABORATE|IMPLEMENT) \\|\\| 'claude-sonnet-5-5' \\}\\}" <<<"$a" && ok "$n: the model comes from a variable with a cheap default" || bad "$n: model not configurable"
  # ---- safety: untrusted text never reaches a shell or the agent directly
  for field in comment.body issue.body issue.title; do
    yq -r '.. | select(type == "!!str")' "$f" | grep -v '^\s*#' | grep -q "github.event.$field" && {
      # allowed only as the value of an env entry, never inside a run script
      yq -r '.jobs[].steps[] | select(has("run")) | .run' "$f" | grep -q "github.event.$field" && bad "$n: github.event.$field is interpolated into a script" || true
    }
  done
  ok "$n: untrusted event fields are never interpolated into a run script"
  p=$(prompt "$f")
  grep -q "data only" <<<"$p" && ok "$n: the prompt says issue text is data" || bad "$n: prompt lacks the data-only rule"
  grep -q "issue.md" <<<"$p" && ok "$n: the agent reads the prepared file" || bad "$n: prompt does not use the prepared file"
  grep -qE 'Bash\(gh (issue view|api|release|repo)|Bash\(curl|Bash\(wget|Bash\(git tag|gh pr (merge|review|close)|gh issue close|git push origin main|git push --force|Bash\(rm' <<<"$a" && bad "$n: a dangerous command is allowed" || ok "$n: no view/api/release/merge/tag/force-push/curl in the tool list"
  eq "$n: the usage report runs even when the agent fails" "$(y "$f" '.jobs.agent.steps[] | select(.name == "Report what the run used") | .if')" "always()"
  y "$f" '.jobs.agent.steps[] | select(.id == "claude") | .uses' | grep -qE '@[0-9a-f]{40}$' && ok "$n: the action is pinned to a commit" || bad "$n: action not pinned"
done

# ---- who can start what
# every new issue reaches the (free) gate, which elaborates the maintainers' and leaves a note on the others' (never silent)
y "$E" '.jobs.gate.if' | grep -qE "^ *github.event_name == 'issues' \|\|$" && ok "elaborate: every opened issue reaches the gate (it tells the maintainers' from the rest)" || bad "elaborate: issues filter"
y "$E" '.jobs.gate.if' | grep -qF "startsWith(github.event.comment.body, '/elaborate')" && ok "elaborate: /elaborate reaches the gate (it checks who)" || bad "elaborate: /elaborate filter"
y "$E" '.jobs.gate.if' | grep -q "needs-info" && y "$E" '.jobs.gate.if' | grep -qF "contains(fromJSON('[\"OWNER\",\"MEMBER\",\"COLLABORATOR\"]'), github.event.comment.author_association)" \
  && ok "elaborate: answers run it only on needs-info issues and only from the owner or a collaborator" || bad "elaborate: needs-info filter"
# /implement stays owner only (the owner's decision): anyone's /implement reaches the gate, which refuses with the reason, and the agent
# job checks the owner again, so that a mistake in the gate can never let anyone else start an implementation
y "$I" '.jobs.gate.if' | grep -qF "startsWith(github.event.comment.body, '/implement')" && ! y "$I" '.jobs.gate.if' | grep -q "author_association" \
  && ok "implement: /implement of anyone reaches the gate, which says why it refuses" || bad "implement: command filter"
{ y "$I" '.jobs.agent.if' | grep -qF "github.event.comment.author_association == 'OWNER'" && y "$I" '.jobs.agent.if' | grep -qF "github.event_name == 'workflow_dispatch' && github.actor == github.repository_owner"; } \
  && ok "implement: the agent job itself runs only for the owner" || bad "implement: agent job not owner-only"
grep -qF 'g.skip("/implement is for the owner"' "$here/internal/gate/gate.go" && ok "implement: the gate refuses anyone but the owner" || bad "implement: gate owner check"
eq "implement: comments and a manual run start it" "$(y "$I" '.on | keys | sort | join(",")')" "issue_comment,workflow_dispatch"
eq "elaborate: opened issues, comments and a manual run start it" "$(y "$E" '.on | keys | sort | join(",")')" "issue_comment,issues,workflow_dispatch"
# ---- manual runs from a branch (workflow_dispatch): the owner only, with an issue number, through the same gate
for f in "$E" "$I"; do
  n=$(basename "$f")
  eq "$n: a manual run needs the issue number" "$(y "$f" '.on.workflow_dispatch.inputs.issue.required')" "true"
  y "$f" '.jobs.gate.if' | grep -qF "github.event_name == 'workflow_dispatch' && github.actor == github.repository_owner" && ok "$n: a manual run is for the owner only" || bad "$n: manual run not owner-only"
  y "$f" '.jobs.gate.steps[] | select(.id == "g") | .env.ACTOR_ASSOC' | grep -qF "workflow_dispatch' && 'OWNER'" && ok "$n: the gate is told a manual run is the owner's (the filter checked it)" || bad "$n: ACTOR_ASSOC for dispatch"
  for field in inputs.instructions inputs.issue; do
    yq -r '.jobs[].steps[] | select(has("run")) | .run' "$f" | grep -q "$field" && bad "$n: $field is interpolated into a script" || true
  done
  ok "$n: the manual inputs never reach a run script"
done
eq "implement: instructions are optional" "$(y "$I" '.on.workflow_dispatch.inputs.instructions.required')" "false"
y "$I" '.jobs.gate.steps[] | select(.id == "g") | .env.COMMENT_BODY' | grep -qF "format('/implement {0}', inputs.instructions)" && ok "implement: the instructions of a manual run go through the same /implement path" || bad "implement: instructions path"

# ---- tools: elaboration is read-only; implementation cannot touch its own guards or the release path
# the elaboration agent cannot write files, and a body passed in a shell argument is rejected when a line starts with '#': bold section titles
sec=$(sed -n '/^## Elaborating new issues/,/^## Implementing an issue/p' "$here/AGENTS.md")
{ grep -qF "gh issue edit <n> --body '" <<<"$sec" && grep -q 'single quotes' <<<"$sec" && grep -q 'in bold' <<<"$sec" && ! grep -q 'body-file' <<<"$sec"; } && ok "AGENTS.md: the body is passed as text between single quotes with bold section titles (no files)" || bad "AGENTS.md: body rule"
grep -q 'cannot write files' <<<"$(prompt "$E")" && grep -q 'single' <<<"$(prompt "$E")" && grep -q 'bold' <<<"$(prompt "$E")" && ok "elaborate: the prompt says: no files, single quotes, bold titles" || bad "elaborate: prompt lacks the body rule"
ae=$(args "$E"); ai=$(args "$I")
grep -qE 'Edit|Write|Bash\(git|Bash\(bash|gh pr create' <<<"$ae" && bad "elaborate: the agent can change files or run code" || ok "elaborate: read-only tools, plus issue edit and comment"
# a proposal (an issue from outside the project) can only comment: its tool list has no issue edit and no labels
tp=$(sed -nE "s/.*mode == 'proposal' && '([^']*)' \|\| '([^']*)'.*/\1/p" <<<"$ae"); te=$(sed -nE "s/.*mode == 'proposal' && '([^']*)' \|\| '([^']*)'.*/\2/p" <<<"$ae")
eq "elaborate: in proposal mode the only tool that writes is a comment" "$tp" "Read,Grep,Glob,Bash(gh issue comment:*)"
eq "elaborate: otherwise the tools are as before (read, issue edit and comment, label list)" "$te" "Read,Grep,Glob,Bash(gh issue edit:*),Bash(gh issue comment:*),Bash(gh label list:*)"
eq "elaborate: the gate's mode reaches the agent job" "$(y "$E" '.jobs.gate.outputs.mode')" '${{ steps.g.outputs.mode }}'
{ grep -qF 'needs.gate.outputs.mode' <<<"$(prompt "$E")" && grep -qF '**Next:**' <<<"$(prompt "$E")" && grep -q 'at most three questions' <<<"$(prompt "$E")"; } \
  && ok "elaborate: the prompt gives the mode, asks for at most three questions and the Next line" || bad "elaborate: prompt lacks mode, questions or Next"
eq "elaborate: the report knows the mode (a proposal's last line differs)" "$(y "$E" '.jobs.agent.steps[] | select(.name == "Report what the run used") | .env.MODE')" '${{ needs.gate.outputs.mode }}'
grep -qF '**Next:**' <<<"$(prompt "$I")" && ok "implement: the prompt asks for the Next line on the issue" || bad "implement: prompt lacks Next"
eq "elaborate: the agent job does not persist the checkout credentials" "$(y "$E" '.jobs.agent.steps[0].with."persist-credentials"')" "false"
grep -qF 'Bash(git push -u origin agent/*)' <<<"$ai" && ok "implement: it can push only agent/ branches" || bad "implement: push rule"
# a rule `Bash(prefix/:*)` needs a word boundary after the prefix and never matches `prefix/file` (it denied the first real run): path-like prefixes use `*`
grep -qE 'Bash\([^)]*/:\*\)' <<<"$ai$ae" && bad "a Bash rule ends in '/:*', which matches nothing" || ok "no Bash rule ends in '/:*' (it would deny real commands)"
grep -qF 'Bash(bash tests/*)' <<<"$ai" && ok "implement: it can run the test files (bash tests/<name>.test.sh)" || bad "implement: tests cannot be run"
grep -q 'no pipes' <<<"$(prompt "$I")" && ok "implement: the prompt asks for one simple command per call" || bad "implement: prompt lacks the one-command rule"
grep -qE 'git push:\*|git push origin|git push -u origin [^a]' <<<"$ai" && bad "implement: a broad git push is allowed" || ok "implement: no broad git push"
for guard in ".github/workflows/claude-" "internal/gate/" "cmd/tfpls/cmd_gate.go" "AGENTS.md"; do
  grep -q "disallowedTools.*Edit($guard" <<<"$ai" && grep -q "Write($guard" <<<"$ai" && ok "implement: it cannot edit $guard" || bad "implement: $guard is editable"
done
[ "$(y "$I" '.permissions."pull-requests"')" = write ] && [ "$(y "$I" '.permissions.contents')" = write ] && ok "implement: it can push and open a pull request" || bad "implement: permissions"
[ "$(y "$E" '.permissions.contents')" = read ] && ok "elaborate: contents read only" || bad "elaborate: contents permission"
y "$I" '.jobs.agent.steps[] | select(.id == "claude") | .env' | grep -q "GH_TOKEN" && bad "implement: GH_TOKEN is forced to the workflow token (its pull requests would not run the checks)" || ok "implement: gh uses the action's app token (the pull request's checks run)"
[ "$(y "$I" '.jobs.agent.timeout-minutes')" -le 30 ] && ok "implement: at most 30 minutes" || bad "implement: timeout"
[ "$(y "$E" '.jobs.agent.timeout-minutes')" -le 10 ] && ok "elaborate: at most 10 minutes" || bad "elaborate: timeout"
y "$I" '.jobs.agent.steps[] | select(.name == "Git identity for the commits") | .run' | grep -q 'users.noreply.github.com' && ok "implement: commits use the owner's noreply identity (never a hostname fallback)" || bad "implement: git identity"
same=$(grep -ho 'anthropics/claude-code-action@[0-9a-f]\{40\}' "$E" "$I" "$W/claude-triage.yml" | sort -u | wc -l | tr -d ' ')
eq "the Claude action is the same pinned commit in triage, elaboration and implementation" "$same" "1"

# ---- documents the agents and the humans follow
grep -q 'empty template checkbox' "$here/AGENTS.md" && grep -q 'never counts as open boxes' "$here/AGENTS.md" && ok "AGENTS.md: empty placeholder boxes of the preserved original become plain lines" || bad "AGENTS.md: placeholder rule missing"
grep -q 'workflow file of the run differs' "$here/CONTRIBUTING.md" && ok "CONTRIBUTING: says what a manual run from a branch can and cannot test" || bad "CONTRIBUTING lacks the limit of manual runs"
grep -q '^## Elaborating new issues' "$here/AGENTS.md" && grep -q '^## Implementing an issue' "$here/AGENTS.md" && ok "AGENTS.md has both sets of rules" || bad "AGENTS.md sections"
# defaults first, at most three questions (one number everywhere), the Next line, proposal mode
{ grep -q 'Defaults first' <<<"$sec" && grep -q 'at most three' <<<"$sec" && grep -q 'Assumptions (reply to change any)' <<<"$sec" && ! grep -qi 'at most five' "$here/AGENTS.md"; } \
  && ok "AGENTS.md: defaults first, assumptions, at most three questions (no 'five' left)" || bad "AGENTS.md: defaults-first rule"
[ "$(grep -c '^   - .*\*\*Next:\*\*' <<<"$sec")" -eq 3 ] && ok "AGENTS.md: the three Next lines (ready, needs-info, proposal)" || bad "AGENTS.md: Next lines"
grep -q 'Proposal mode' <<<"$sec" && grep -q 'Never edit the body or the labels' <<<"$sec" && ok "AGENTS.md: proposal mode comments only" || bad "AGENTS.md: proposal mode"
tri=$(sed -n '/^## Triage of new issues/,/^## Elaborating new issues/p' "$here/AGENTS.md")
{ grep -q '\*\*Next:\*\*' <<<"$tri" && ! grep -q 'skeleton comment\|add one comment with a proposed' <<<"$tri"; } && ok "AGENTS.md: the triage comment ends with Next and proposes no body (one comment per run)" || bad "AGENTS.md: triage rules"
# the flow in four steps and who can do what, in AGENTS.md and in CONTRIBUTING.md
flow=$(sed -n '/^## The issue flow/,/^## Triage of new issues/p' "$here/AGENTS.md")
{ [ "$(grep -cE '^[1-4]\. \*\*' <<<"$flow")" -eq 4 ] && grep -qF '| Who | Opens an issue | Comments on a `needs-info` issue | `/elaborate` | `/implement` |' <<<"$flow"; } \
  && ok "AGENTS.md: the issue flow in four steps and the who-can-do-what table" || bad "AGENTS.md: issue flow"
ia=$(sed -n '/^## Issue agent (maintainers)/,$p' "$here/CONTRIBUTING.md")
{ grep -q 'The flow in four steps' <<<"$ia" && [ "$(grep -cE '^[1-4]\. \*\*' <<<"$ia")" -eq 4 ] && grep -q 'Who can do what' <<<"$ia" \
  && grep -q 'Any comment of the owner or a collaborator on an issue labelled `needs-info` runs the agent again' <<<"$ia" \
  && grep -q '| `/implement \[instructions\]` | the implementation starts on a `ready` issue | refused with the reason | refused with the reason |' <<<"$ia"; } \
  && ok "CONTRIBUTING: four steps, who can do what, how an answer runs the agent again, /implement owner only" || bad "CONTRIBUTING: issue agent flow"
grep -qiE 'never.*merge' "$here/AGENTS.md" && grep -q 'Refs #<n>' "$here/AGENTS.md" && grep -qi 'cost' "$here/AGENTS.md" && ok "AGENTS.md says: never merge, Refs not Closes, and the cost rules" || bad "AGENTS.md rules"
t="$here/.github/pull_request_template.md"
{ grep -q '^## TLDR' "$t" && grep -q '\*\*What:\*\*' "$t" && grep -q '\*\*Why:\*\*' "$t" && grep -q '\*\*How:\*\*' "$t"; } && ok "the pull request template starts with a TLDR (what, why, how)" || bad "TLDR missing"
for h in "Decisions and questions" "Verification" "Not verified" "Risks and limitations"; do grep -q "^## $h" "$t" && ok "the template has '$h'" || bad "template lacks '$h'"; done
[ "$(grep -n '^## TLDR' "$t" | cut -d: -f1)" -lt "$(grep -n '^## Change' "$t" | cut -d: -f1)" ] && ok "the TLDR comes first" || bad "TLDR not first"
for v in TFPLS_AGENT_ENABLED TFPLS_AGENT_MODEL_ELABORATE TFPLS_AGENT_MODEL_IMPLEMENT TFPLS_AGENT_MAX_ELAB_PER_ISSUE TFPLS_AGENT_MAX_IMPL_PER_ISSUE TFPLS_AGENT_MAX_ELAB_PER_DAY TFPLS_AGENT_MAX_IMPL_PER_DAY; do
  grep -q "$v" "$here/CONTRIBUTING.md" && grep -q "$v" "$E$I" 2>/dev/null || grep -q "$v" "$E" "$I" && ok "$v is documented and used" || bad "$v not both documented and used"
done

done_tests
