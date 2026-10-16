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

Semantic versioning: breaking changes to inputs, config keys or behaviour callers rely on bump the major version. `tools/release.sh` cuts a release and moves the major tag; see [docs/releasing.md](docs/releasing.md).

## Licence and sign-off

tfpls is source-available under the [Functional Source License, Version 1.1, ALv2 Future License](LICENSE) (FSL-1.1-ALv2): you may use, copy, modify and redistribute it for any purpose except offering a competing commercial product or service, and each release becomes Apache-2.0 two years after it is published.

Contributions are accepted under the same licence, certified with the [Developer Certificate of Origin](https://developercertificate.org/): sign every commit off with `git commit -s` (a `Signed-off-by: Your Name <you@example.com>` line). By signing off you state that you wrote the change or have the right to submit it under this licence. Pull requests with unsigned commits are asked to be amended before merge.

## Issue triage (maintainers)

`.github/workflows/claude-triage.yml` files new issues: milestone, labels `type:*`, `area:*`, `priority:*` and one comment with the proposed priority, size, area, kind and status (rules in [AGENTS.md](AGENTS.md#triage-of-new-issues)). It starts for the owner's new issues and for `/triage` comments by the owner, members and collaborators, never for bots or pull requests. `/triage priority:p1 milestone:M3` gives hints that replace the rules.

One-time setup: `claude setup-token`, then `gh secret set CLAUDE_CODE_OAUTH_TOKEN` in this repository, and install the Claude GitHub app. Without the secret a triage run stops with a comment saying so. A user-owned GitHub Project cannot be written with the workflow's token (and no personal token is used on purpose): the board's built-in "Auto-add to project" and "Item added to project: Status Backlog" workflows put issues on it; the other columns are filled by hand.

Safety: Claude can only read files and run `gh issue view/edit/comment` and `gh label list` (no raw `gh api`, no file edits, no git); issue text is data and instructions in it are ignored; the token has `issues: write` only; the action is pinned to a commit and the model is Haiku.
