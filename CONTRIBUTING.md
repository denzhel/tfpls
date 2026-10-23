# Contributing

## Flow

1. Open an issue (templates in `.github/ISSUE_TEMPLATE`) or comment on an existing one. New issues are triaged by Claude (milestone, labels, a proposed priority and size); comment `/triage` on an issue to have it triaged or re-triaged ([issue triage](#issue-triage-maintainers)). Issues of maintainers are filled in by the [issue agent](#issue-agent-maintainers). For anything beyond a small fix, agree on the approach first.
2. Branch from `main`; one focused change per pull request, using the pull request template.
3. Add or update tests: `bash tests/run-all.sh` runs the whole offline suite (no network, no cloud, stubs for `gh`, `tofu` and `npx`). New behaviour comes with a test that fails without it.
4. Run the linters CI runs: `actionlint` and `shellcheck -S warning actions/bin/install.sh tools/onboard tools/release.sh tools/tfpls-dashboard tests/*.sh`.
5. Update the docs (`docs/`, the README configuration tables, `examples/`) and add a line under *Unreleased* in `CHANGELOG.md`.
6. In the pull request, write what you ran (exact commands and results) and what you did **not** verify. Claims of a passing check must be true.

## Style

- New logic is written in Go (`cmd/tfpls`, one package per module in `internal/<name>/`, standard library first, Go 1.24): `gofmt -l .` prints nothing, `go vet ./...` and `go test ./...` pass. Black-box behaviour stays pinned by `tests/*.test.sh`, which drive the built binary; Go unit tests sit beside the code.
- Shell is only for the bootstrap and installer (`actions/bin/install.sh`, `tools/onboard`), the two wrappers (`tools/release.sh`, `tools/tfpls-dashboard`) and the tests (`set -uo pipefail`, quote everything, no zsh-isms); everything else is Go. Composite actions are for reusable steps; keep workflow YAML thin.
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

Two workflows turn an issue into a pull request; a person reviews and merges it. The agent never stays silent: a skip that a person can act on is said on the issue, and every comment of the agents ends with a line **Next:** that says what happens next. The rules the agents follow are in [AGENTS.md](AGENTS.md).

**The flow in four steps**

1. **Open an issue.** A template or a few lines are enough.
2. **The agent fills it in** (`.github/workflows/claude-elaborate.yml`, for issues of the owner and collaborators): Outcome, Tasks, Acceptance criteria, Test criteria, Dependencies and Context, from the description and the code (the original text is kept in a collapsed block). It decides what it can, lists those choices as **assumptions** and labels the issue `ready`. Only for a real blocker (a secret, an account, a product decision without a safe default, contradicting requirements) it asks, at most three questions with a recommended answer each, and labels the issue `needs-info`. An issue that is already filled in (the tracker agent writes full ones) is labelled `ready` by the gate, without a model run.
3. **Answer if asked.** Reply in a comment; a plain "yes" takes the defaults. **Any comment of the owner or a collaborator on an issue labelled `needs-info` runs the agent again** (a comment that starts with a command such as `/triage` does not). On an issue without `needs-info` a comment starts nothing: to change an assumption on a `ready` issue, or to have the agent look at an issue again, comment `/elaborate` with what to change.
4. **`/implement`** (`.github/workflows/claude-implement.yml`), when the issue is `ready` and you want it built: **only the owner** comments `/implement` (the owner's decision), optionally with instructions after it, which can also override an assumption. The agent works on `agent/issue-<n>`: tests first, the change, docs and changelog, the checks, then it opens a pull request with a **TLDR (what, why, how)**, the decisions, the verification, a "not verified" list and the risks. It never merges, releases, tags or closes the issue, and cannot edit its own workflows, `internal/gate/` or `AGENTS.md`. A filled-in issue without open questions that lacks the label gets `ready` from the gate; anyone but the owner, and `/implement` on an issue with questions or one that is not filled in, gets the reason in a comment.

**Who can do what**

| | Owner | Collaborator or member | Anyone else |
|---|---|---|---|
| Opens an issue | the agent fills it in (and the triage runs) | the agent fills it in | a short note, no model run |
| Comments on a `needs-info` issue | the agent runs again | the agent runs again | nothing runs |
| `/elaborate [instructions]` | the agent runs | the agent runs | refused with the reason |
| `/implement [instructions]` | the implementation starts on a `ready` issue | refused with the reason | refused with the reason |
| `/triage [hints]` | the triage runs | the triage runs | nothing runs |
| Manual run from a branch | yes | no | no |

An issue written outside the project is never elaborated by itself, because its text could carry instructions. A maintainer who wants help with one comments `/elaborate`: the agent then posts a **proposal** as one comment, reads the outsider's title, body and comments only as fenced data, and cannot edit the body or the labels. The agent never implements such an issue; to build it, the owner opens a new issue with the agreed text.

What the gate says when no model runs, once per reason (and again after a later run of the agent): an issue that is already filled in (it is labelled `ready`), an issue from outside the project (the note), `/elaborate` or `/implement` from someone who may not use it, `/implement` on an issue with open questions or one that is not filled in, and a reached run cap. The kill switch, bots, pull requests, other commands and ordinary discussion stay silent.

Both workflows need the secret `CLAUDE_CODE_OAUTH_TOKEN` and the Claude GitHub app (as the triage). The token is a subscription token: the usage counts against the owner's plan, so cost is controlled in four layers:

| Control | What it does | Variable (default) |
|---|---|---|
| Free pre-filter | the job `if:` starts no runner unless the event is a new issue, `/elaborate`, an answer of the owner or a collaborator on `needs-info`, `/implement`, or the owner's manual run | |
| Gate (`tfpls gate`, `internal/gate`) | no model run when the issue is already filled in, comes from outside the project, is not `ready` or still has questions (implement), the commenter may not start it, or the sender is a bot; it says why on the issue when a person can act on it | |
| Kill switch | stops both workflows at once | `TFPLS_AGENT_ENABLED` (`true`; set `false` to stop) |
| Model | the cheapest sufficient one; triage stays on Haiku | `TFPLS_AGENT_MODEL_ELABORATE`, `TFPLS_AGENT_MODEL_IMPLEMENT` (`claude-sonnet-5-5`) |
| Limits per run | turns and time | elaboration 30 turns / 10 min, implementation 60 turns / 30 min |
| Run caps | the gate refuses and says so on the issue | `TFPLS_AGENT_MAX_ELAB_PER_ISSUE` (3), `TFPLS_AGENT_MAX_IMPL_PER_ISSUE` (2), `TFPLS_AGENT_MAX_ELAB_PER_DAY` (20), `TFPLS_AGENT_MAX_IMPL_PER_DAY` (5) |

Every run ends with one comment on the issue (turns, seconds and cost as reported by Claude Code, and the **Next:** line from the labels the agent left) and a line in the run summary; a failed run is reported too, because it counts toward the caps. A failed run is never retried by itself. Set a variable with `gh variable set NAME --body VALUE --repo OWNER/REPO`.

**Testing a change to the agent from its branch.** The issue and comment events always run the workflows of the default branch, so a change to the agent would have to be merged before it could be tried. The owner can run both workflows by hand from any branch instead; the run uses that branch's workflow file, gate script, prompts and tool list:

```bash
gh workflow run claude-elaborate.yml --ref <branch> -f issue=<n>
gh workflow run claude-implement.yml --ref <branch> -f issue=<n> [-f instructions="..."]
```

What a manual run can and cannot test: the Claude action refuses to run (it skips itself with a warning, and the run reports "the agent produced no result") when the workflow file of the run differs from the one on the default branch; that is a security check, so a branch cannot rewrite the workflow to get at the token. Changes to what the workflow only references run for real from a branch: the gate (`tfpls gate`) and the report (`tfpls agent-report`) (both built from the branch by `actions/bin`), the rules in `AGENTS.md`, the tests and the docs. A change to the workflow file itself (the prompt text in it, the tool lists, the model) can only be tested by the gate and the static tests before the merge, and for real after it.

A manual run goes through the same gate as the events (kill switch, caps, a trusted copy of the issue; `ready` is required for an implementation) and costs the same, so test on a throwaway issue and delete it afterwards. Only the owner can start one. A manual elaboration always runs (it is an explicit request), so the paths of the issue and comment events (already filled in, the note for an outsider, the status comments) are covered by `tests/agent-gate.test.sh` and the Go tests of `internal/gate` before the merge and tried for real after it.

Safety: only the owner and collaborators start an elaboration, and only the owner an implementation (the gate checks it, and the agent job's condition checks it again); only the words of the owner, collaborators and our own bots reach the agent as they are (a prepared file; it cannot call `gh issue view`), and an outsider's text reaches it only as fenced data in a proposal run, whose only tool that writes is a comment; the tools are an explicit list; neither workflow uses `pull_request_target`. A workflow cannot write the Project board, so the board is synced from the labels afterwards.
