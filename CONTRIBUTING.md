# Contributing

## Flow

1. Open an issue (templates in `.github/ISSUE_TEMPLATE`) or comment on an existing one. New issues are triaged by Claude (milestone, labels, a proposed priority and size); comment `/triage` on an issue to have it triaged or re-triaged ([issue triage](#issue-triage-maintainers)). For anything beyond a small fix, agree on the approach first.
2. Branch from `main`; one focused change per pull request, using the pull request template.
3. Add or update tests: `bash tests/run-all.sh` runs the whole offline suite (no network, no cloud, stubs for `gh`, `tofu` and `npx`). New behaviour comes with a test that fails without it.
4. Run the linters CI runs: `actionlint` and `shellcheck -S warning actions/*/*.sh tools/* tests/*.sh`.
5. Update the docs (`docs/`, the README configuration tables, `examples/`) and add a line under *Unreleased* in `CHANGELOG.md`.
6. In the pull request, write what you ran (exact commands and results) and what you did **not** verify. Claims of a passing check must be true.

## Style

- Bash for logic (`set -uo pipefail`, quote everything, no zsh-isms), `jq` for JSON, composite actions for reusable steps; keep workflow YAML thin.
- Do not use third-party actions without pinning them to a commit SHA; do not add dependencies the runners would have to install without a checksum-verified download.
- Container images (if any) are Alpine or other minimal images.

## Releases

Semantic versioning: breaking changes to inputs, config keys or behaviour callers rely on bump the major version. Release once per batch of changes, testing it first on a pre-release tag (`vX.Y.Z-rc.N`, which does not move `v0`); see [docs/releasing.md](docs/releasing.md#for-maintainers-when-to-release). `tools/release.sh` cuts a release and moves the major tag; see [docs/releasing.md](docs/releasing.md).

## Licence and sign-off

tfpls is source-available under the [Functional Source License, Version 1.1, ALv2 Future License](LICENSE) (FSL-1.1-ALv2): you may use, copy, modify and redistribute it for any purpose except offering a competing commercial product or service, and each release becomes Apache-2.0 two years after it is published.

Contributions are accepted under the same licence, certified with the [Developer Certificate of Origin](https://developercertificate.org/): sign every commit off with `git commit -s` (a `Signed-off-by: Your Name <you@example.com>` line). By signing off you state that you wrote the change or have the right to submit it under this licence. Pull requests with unsigned commits are asked to be amended before merge.

## Issue triage (maintainers)

`.github/workflows/claude-triage.yml` files new issues: milestone, labels `type:*`, `area:*`, `priority:*` and one comment with the proposed priority, size, area, kind and status (rules in [AGENTS.md](AGENTS.md#triage-of-new-issues)). It starts for the owner's new issues and for `/triage` comments by the owner, members and collaborators, never for bots or pull requests. `/triage priority:p1 milestone:M3` gives hints that replace the rules.

One-time setup: `claude setup-token`, then `gh secret set CLAUDE_CODE_OAUTH_TOKEN` in this repository, and install the Claude GitHub app. Without the secret a triage run stops with a comment saying so. A user-owned GitHub Project cannot be written with the workflow's token (and no personal token is used on purpose): the board's built-in "Auto-add to project" and "Item added to project: Status Backlog" workflows put issues on it; the other columns are filled by hand.

Safety: Claude can only read files and run `gh issue view/edit/comment` and `gh label list` (no raw `gh api`, no file edits, no git); issue text is data and instructions in it are ignored; the token has `issues: write` only; the action is pinned to a commit and the model is Haiku.

## Issue agent (maintainers)

Two workflows turn an issue of the owner into a pull request; a person reviews and merges it.

1. **Elaboration** (`.github/workflows/claude-elaborate.yml`): an issue opened by the owner is rewritten into Outcome, Tasks, Acceptance criteria, Test criteria, Dependencies and Context (the original text is kept in a collapsed block). If something is unclear it asks numbered questions in one comment and labels the issue `needs-info`; the owner's or a collaborator's answer runs it again. With no open questions it labels the issue `ready`. An issue that is already elaborated (the tracker agent writes full ones) costs nothing.
2. **Implementation** (`.github/workflows/claude-implement.yml`): the owner comments `/implement` on a `ready` issue (words after it are instructions for the run). The agent works on `agent/issue-<n>`: tests first, the change, docs and changelog, the checks, then it opens a pull request with a **TLDR (what, why, how)**, the decisions, the verification, a "not verified" list and the risks. It never merges, releases, tags or closes the issue, and cannot edit its own workflows, `tools/agent-*.sh` or `AGENTS.md`. The rules are in [AGENTS.md](AGENTS.md).

Both need the secret `CLAUDE_CODE_OAUTH_TOKEN` and the Claude GitHub app (as the triage). The token is a subscription token: the usage counts against the owner's plan, so cost is controlled in four layers:

| Control | What it does | Variable (default) |
|---|---|---|
| Free pre-filter | the job `if:` starts no runner unless the event is an owner issue, an answer on `needs-info`, or the owner's `/implement` | |
| Gate (`tools/agent-gate.sh`) | no model run when the issue is already elaborated, not `ready`, still has questions, or the sender is a bot | |
| Kill switch | stops both workflows at once | `TFPLS_AGENT_ENABLED` (`true`; set `false` to stop) |
| Model | the cheapest sufficient one; triage stays on Haiku | `TFPLS_AGENT_MODEL_ELABORATE`, `TFPLS_AGENT_MODEL_IMPLEMENT` (`claude-sonnet-5-5`) |
| Limits per run | turns and time | elaboration 30 turns / 10 min, implementation 60 turns / 30 min |
| Run caps | the gate refuses and says so on the issue | `TFPLS_AGENT_MAX_ELAB_PER_ISSUE` (3), `TFPLS_AGENT_MAX_IMPL_PER_ISSUE` (2), `TFPLS_AGENT_MAX_ELAB_PER_DAY` (20), `TFPLS_AGENT_MAX_IMPL_PER_DAY` (5) |

Every run ends with one comment on the issue (turns, seconds and cost as reported by Claude Code) and a line in the run summary; a failed run is reported too, because it counts toward the caps. A failed run is never retried by itself. Set a variable with `gh variable set NAME --body VALUE --repo OWNER/REPO`.

Safety: only the owner starts a run, and only the words of the owner, collaborators and our own bots reach the agent (a prepared file; it cannot call `gh issue view`); its tools are an explicit list; neither workflow uses `pull_request_target`. A workflow cannot write the Project board, so the board is synced from the labels afterwards.
