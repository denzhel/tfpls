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
  eq "$n: the model job only runs when the gate says so" "$(y "$f" '.jobs.agent.if')" "needs.gate.outputs.run == 'true'"
  eq "$n: the gate is the script that is tested offline" "$(y "$f" '.jobs.gate.steps[] | select(.id == "g") | .run' | grep -c 'tools/agent-gate.sh')" "1"
  [ "$(y "$f" '.jobs.gate.timeout-minutes')" -le 5 ] && ok "$n: the gate job is short" || bad "$n: gate timeout"
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
y "$E" '.jobs.gate.if' | grep -q "author_association == 'OWNER'" && ok "elaborate: an opened issue only from the owner" || bad "elaborate: owner filter"
y "$E" '.jobs.gate.if' | grep -q "needs-info" && ok "elaborate: answers only on needs-info issues" || bad "elaborate: needs-info filter"
y "$I" '.jobs.gate.if' | grep -q "comment.author_association == 'OWNER'" && ok "implement: only the owner" || bad "implement: owner filter"
y "$I" '.jobs.gate.if' | grep -q "'/implement'" && ok "implement: the exact command" || bad "implement: command filter"
eq "implement: only comments start it" "$(y "$I" '.on | keys | join(",")')" "issue_comment"
eq "elaborate: opened issues and comments start it" "$(y "$E" '.on | keys | sort | join(",")')" "issue_comment,issues"

# ---- tools: elaboration is read-only; implementation cannot touch its own guards or the release path
ae=$(args "$E"); ai=$(args "$I")
grep -qE 'Edit|Write|Bash\(git|Bash\(bash|gh pr create' <<<"$ae" && bad "elaborate: the agent can change files or run code" || ok "elaborate: read-only tools, plus issue edit and comment"
grep -qF 'Bash(git push -u origin agent/*)' <<<"$ai" && ok "implement: it can push only agent/ branches" || bad "implement: push rule"
# a rule `Bash(prefix/:*)` needs a word boundary after the prefix and never matches `prefix/file` (it denied the first real run): path-like prefixes use `*`
grep -qE 'Bash\([^)]*/:\*\)' <<<"$ai$ae" && bad "a Bash rule ends in '/:*', which matches nothing" || ok "no Bash rule ends in '/:*' (it would deny real commands)"
grep -qF 'Bash(bash tests/*)' <<<"$ai" && ok "implement: it can run the test files (bash tests/<name>.test.sh)" || bad "implement: tests cannot be run"
grep -q 'no pipes' <<<"$(prompt "$I")" && ok "implement: the prompt asks for one simple command per call" || bad "implement: prompt lacks the one-command rule"
grep -qE 'git push:\*|git push origin|git push -u origin [^a]' <<<"$ai" && bad "implement: a broad git push is allowed" || ok "implement: no broad git push"
for guard in ".github/workflows/claude-" "tools/agent-" "AGENTS.md"; do
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
grep -q '^## Elaborating new issues' "$here/AGENTS.md" && grep -q '^## Implementing an issue' "$here/AGENTS.md" && ok "AGENTS.md has both sets of rules" || bad "AGENTS.md sections"
grep -qiE 'never.*merge' "$here/AGENTS.md" && grep -q 'Refs #<n>' "$here/AGENTS.md" && grep -qi 'cost' "$here/AGENTS.md" && ok "AGENTS.md says: never merge, Refs not Closes, and the cost rules" || bad "AGENTS.md rules"
t="$here/.github/pull_request_template.md"
{ grep -q '^## TLDR' "$t" && grep -q '\*\*What:\*\*' "$t" && grep -q '\*\*Why:\*\*' "$t" && grep -q '\*\*How:\*\*' "$t"; } && ok "the pull request template starts with a TLDR (what, why, how)" || bad "TLDR missing"
for h in "Decisions and questions" "Verification" "Not verified" "Risks and limitations"; do grep -q "^## $h" "$t" && ok "the template has '$h'" || bad "template lacks '$h'"; done
[ "$(grep -n '^## TLDR' "$t" | cut -d: -f1)" -lt "$(grep -n '^## Change' "$t" | cut -d: -f1)" ] && ok "the TLDR comes first" || bad "TLDR not first"
for v in TFPLS_AGENT_ENABLED TFPLS_AGENT_MODEL_ELABORATE TFPLS_AGENT_MODEL_IMPLEMENT TFPLS_AGENT_MAX_ELAB_PER_ISSUE TFPLS_AGENT_MAX_IMPL_PER_ISSUE TFPLS_AGENT_MAX_ELAB_PER_DAY TFPLS_AGENT_MAX_IMPL_PER_DAY; do
  grep -q "$v" "$here/CONTRIBUTING.md" && grep -q "$v" "$E$I" 2>/dev/null || grep -q "$v" "$E" "$I" && ok "$v is documented and used" || bad "$v not both documented and used"
done

done_tests
