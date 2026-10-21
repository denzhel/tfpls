# Changelog

All notable changes to tfpls. Versions follow SemVer; consumers pin with `@v0` or an exact tag (see docs/releasing.md).

Versions below 1.0 (0.x): the interface may still change between minor versions; read the notes before updating.

## Unreleased

- Maintainers: `actions/record` is now `tfpls record` (Go, `internal/record`; the data-branch writer is `internal/datastore`). Same event, same messages; the token now reaches git through its environment instead of the remote URL, a branch name that git could read as an option is refused, and the summary is cut at 300 characters instead of 300 bytes.
- Maintainers: `actions/wait-for` is now `tfpls wait-for` (Go, `internal/waitfor`); same inputs and messages, and an unparsable job list is a read failure that is retried instead of being taken as "no jobs".

- Maintainers: `tests/bin-install.test.sh` no longer fails about 1 run in 16 (its corrupted checksum replaced the first character by `0`, which changed nothing when the hash already began with `0`).
- Maintainers: the Go port reads YAML with `go.yaml.in/yaml/v3` through `internal/yamlconf` (what `yq -o=json` gave the scripts; checked against `yq` on every workflow, action and example file of the repository); it is the only dependency besides the standard library.
- Maintainers: shared Go helpers for the port: `internal/ghactions` (step outputs with a delimiter that cannot be forged, environment, PATH, summary, escaped annotations, masks) and `internal/gh` (the `gh` CLI as a process); each subcommand registers itself in `cmd/tfpls/cmd_<name>.go`.
- Maintainers: `actions/codeowners` runs the Go `tfpls codeowners enforce` (`internal/codeowners`; `check.sh` and `enforce.sh` are gone; `tfpls codeowners check` is the checker). Same inputs and messages. Fixes: the base ref is URL-escaped in the CODEOWNERS lookup, a non-numeric `comment_id` never reaches the API, a team slug with odd characters is never sent to the API, an entry without `owner_paths` no longer becomes the path `null`, and an empty list of paths is refused with a clear message instead of a usage error.
- Maintainers: `dashboard` runs as `tfpls dashboard` (Go, `internal/dashboard`, the page template is embedded in the binary); `tools/tfpls-dashboard` calls `tfpls dashboard open`. The action refuses an `out` outside the workspace, and the summary table escapes `|` in workspace names.
- Maintainers: `actions/detect-workspaces` runs the Go `tfpls detect` (`internal/detect`) instead of Bash and jq (`detect.jq`, `validate.jq` are gone); same inputs, outputs, messages and exit codes. `tfpls validate-config` prints the config problems of a YAML/JSON file as JSON (used by `actions/approvals`). Fixes: changed files are read NUL-separated (a path with a space or quote now matches its workspace), a workspace named `.` or `..` is refused, a base or head ref that looks like an option is refused, an invalid `lock_timeout`/`notify_secret`/name with a trailing newline no longer passes, an unreadable `workspaces_file` fails instead of selecting nothing, and `workspaces_glob` no longer splits on spaces or lists dot directories.
- Maintainers: `pr-notes` and `mark-outdated` run as `tfpls pr-notes` and `tfpls mark-outdated` (Go, `internal/prcomments`); an unreadable `names` list now marks nothing instead of marking every plan outdated.
- Maintainers: `finish` runs as `tfpls finish webhook` and `tfpls finish store` (Go: `internal/finish`, on `internal/record` and `internal/datastore`).

- Maintainers: `drift-report` runs as `tfpls drift-report` (Go, `internal/driftreport`, on `internal/datastore`).

## v0.6.0

- **Fixed:** `actions/result` could not be used at all: GitHub reads `${{ toJSON(needs) }}` in an input description as an expression and refused to load the action ("Unrecognized named-value: needs"). The description is reworded, and `tests/action-descriptions.test.sh` fails on an expression in any action's input description. If you worked around this, remove the workaround.
- **Changed:** `actions/result` runs `tfpls result` (Go) instead of a shell script with `jq`; same outcome for every `needs` input, and invalid input now fails (it is red, never silently green). The new `actions/bin` installs the `tfpls` binary in a job: the verified release binary on a release tag (SHA-256 and build provenance), built from the ref's own source on `main` and branches; `tools/release.sh` stamps the version in `actions/bin/VERSION`. Callers change nothing. Measured on a hosted runner: about 4 seconds for the release binary with its provenance check (cached afterwards), about 14 seconds to build from source on `@main`. A job pinned to a tag started within about a minute of the tag push can fail to download until the release workflow has published the assets; re-run it.
- Maintainers: the release workflow runs only for exact version tags (`vX.Y.Z`, `vX.Y.Z-rc.N`); moving the major tag `v0` no longer starts a release run that fails.

## v0.5.0

- **Changed:** onboarding is now Go (`tfpls onboard`, `internal/onboard`; same questions, words, files and exit codes, proven by the 110 black-box checks and a side-by-side run of the old script and the binary). `tools/onboard` keeps its URL and the `curl ... | bash` one-liner but becomes a 54-line bootstrap that downloads the release binary, refuses a checksum mismatch, caches it and runs it; `TFPLS_ONBOARD_BIN` runs a local binary offline. It needs a release that carries the binaries (see docs/getting-started.md).
- Maintainers: a pushed release tag (`v*`, pre-releases too) now publishes the `tfpls` binary: `tfpls_<os>_<arch>.tar.gz` for linux and darwin on amd64 and arm64 plus `SHA256SUMS`, with a build provenance attestation (`.github/workflows/release.yml`, `tools/build-release.sh`; verification in `docs/releasing.md`). CI has a `go` job (gofmt, vet, test, cross-build) and Dependabot watches the Go module.
- Maintainers: the Go module `github.com/denzhel/tfpls` and the `tfpls` binary skeleton (`tfpls version`) are in; the port of the tools to Go starts with onboarding, the Bash stays until each module has parity.
- Maintainers: the onboarding pty test in `tests/onboard.test.sh` no longer fails by chance (it killed a finished child when the terminal reported its end a moment before the child could be reaped, about 1 run in 150 on a busy machine); the helper now waits for the exit, and three checks pin the helper itself.

## v0.4.0

- **Changed (breaking for a per-workspace webhook):** `notify_secret` now names a **key** of one secret, `TFPLS_NOTIFY_WEBHOOKS` (a JSON object `{"KEY": "https://..."}`), instead of a secret of its own. The workflows looked the secret up by a computed name (`secrets[notify_secret]`), and that makes GitHub hand every secret of the caller (with `secrets: inherit`: every repository and organisation secret) to the jobs that run OpenTofu, including the `/tofu plan` job that runs pull request code (CodeQL `actions/excessive-secrets-exposure`). Now every job gets only the secrets it names. The default `SLACK_WEBHOOK` is unchanged. **Migrate** if a workspace sets `notify_secret`: create the secret `TFPLS_NOTIFY_WEBHOOKS` with one entry per name your config uses, e.g. `{"SLACK_WEBHOOK_ONCALL": "<the URL from the secret SLACK_WEBHOOK_ONCALL>"}`; the config stays as it is; then delete the old secrets. Until then those workspaces send nothing and the run log warns. The reusable workflows declare exactly the secrets they use: `tofu.yml` declares all four (`TFPLS_ENV_JSON`, `PLAN_ENCRYPTION_KEY`, `SLACK_WEBHOOK`, `TFPLS_NOTIFY_WEBHOOKS`), so a caller may list them instead of `secrets: inherit`; `plan.yml` and `locks.yml` no longer declare `SLACK_WEBHOOK`, which they never used. Maintainers: `tests/secrets-exposure.test.sh` fails on a computed `secrets[...]`, on `toJSON(secrets)` and on an undeclared or unused secret; `tests/finish-webhook.test.sh` covers the lookup and the masking.
- **Docs:** `ubuntu-latest` moves to Ubuntu 26.04 between 2026-10-19 and 2026-11-19: what differs, what was checked (tfpls CI and a full plan, apply, merge and dashboard cycle on `ubuntu-26.04`), and how to pin `ubuntu-24.04` (`docs/runner-minutes.md`).
- **Changed:** `tools/onboard` v2. It says what it understood (layout, backend and locking, providers, OpenTofu version, modules, workflows, CODEOWNERS, default branch), asks before it writes (Enter takes the default; read from the terminal even under `curl | bash`; nothing is asked without a terminal), and is safe to run again: a second run changes nothing, a differing workflow is shown as a diff and replaced only on a yes or with `--force` (before, an existing `tofu.yml` stopped the run with exit 1 unless `--force`), `.tfpls.yml` is never overwritten and no workflow is deleted. New: `--yes`, `--dry-run`, `--no-jokes`; a fourth layout, the one root stack (one root module with a single state becomes one stack named after the repository, on the `default` workspace), with `TFPLS_ENV_JSON` guidance instead of the AWS role steps for a repository without AWS (for example Cloudflare with the state in R2); an optional GitHub step, only on a yes and with `gh`, that reads the numeric IDs for the trust policy, checks the variable and the secrets and sets a generated plan key without showing it; a logo, a joke and a final checklist of what is done and left. See docs/getting-started.md.
- **Fixed:** the apply after a merge no longer tries a reviewed plan that `/tofu apply` already applied. When the bot's newest comment of the workspace on the pull request has the status `applied` and is newer than its newest `planned` one, the run skips that plan, plans once only to confirm that nothing is left, and posts one line: `Already applied by /tofu apply (run); nothing changed since.` (status `no-changes`). Before, it tried the used-up plan, logged `reviewed plan is stale; re-planning`, planned again and posted a second comment headed `plan logs`; with `require_reviewed_plan: true` the run failed. Something left after the apply is handled under `auto_apply` as before (never applied with `require_reviewed_plan: true`), and a commit planned after the apply still has its reviewed plan applied. See "Why did a plan run after my apply?" in `docs/runbooks.md`.
- Maintainers: a simpler issue flow that never stays silent. An issue written from a template with only empty boxes (like #51) is now filled in instead of being taken for elaborated; the agent fills in every section with its defaults, lists them as assumptions, asks at most three questions and only for a real blocker, and labels the issue `ready` otherwise. Every agent comment ends with a **Next:** line, and a skip a person can act on (already filled in, which also gets `ready`; not allowed; a cap) is said once on the issue. Issues of collaborators are filled in on open; `/elaborate` (owner or collaborator) runs the agent; an issue from outside the project gets a short note, and on `/elaborate` a proposal in a comment. `/implement` stays owner-only, and its refusals say what is missing. See CONTRIBUTING.md.
- **Docs:** a shorter README (50 lines instead of 93): why you will like it, see it in sixty seconds, a four-step quick start around the onboarding one-liner, is it safe in three bullets, and where to go next. The feature table and the "Good to know" notes moved to the docs index (`docs/README.md`), which now also links the reference; the hero and the first paragraph are unchanged. Maintainers: `tests/readme.test.sh` keeps them byte for byte and the README under 60 lines.
- **Docs:** "Automated scanning" in `docs/security-model.md`, linked from `SECURITY.md`: which of GitHub's free scanners run on the tfpls repository and when, why on every pull request and also every week, how CodeQL works (queries, no AI) and where AI comes in (Copilot Autofix suggests a fix a person reviews), what it costs, what a private repository gets for free, and what a person still does. Maintainers: CodeQL default setup for the workflows and actions, Dependabot alerts, Dependabot security updates and private vulnerability reporting are now on for this repository (secret scanning and push protection were on already).
- Maintainers: Dependabot keeps the pinned actions current (weekly on Monday 05:00 UTC, minor and patch updates in one pull request, each major on its own, a 5-day cooldown, label `dependencies`, prefix `deps`; nothing merges by itself), and the weekly `tool-versions` workflow keeps one issue "Tool versions behind" open while the default OpenTofu, conftest or actionlint version lags its latest release. **Docs:** new `docs/dependencies.md`, with a ready-to-copy `dependabot.yml` for a caller repository and why a Dependabot pull request needs a maintainer's `/tofu plan`.
- **Changed:** the pinned actions are at their latest releases: `actions/checkout` v7.0.1, `actions/upload-artifact` v7.0.2, `anthropics/claude-code-action` v1.0.253 (all by SHA). Self-hosted runners still need v2.327.1 or newer.
- **Changed:** the default tool versions are the latest: OpenTofu 1.13.1 (was 1.10.6) and conftest 0.71.1 (was 0.56.0). A repository that pins its OpenTofu version (`.opentofu-version`, `.tofu-version`, `.terraform-version` or an exact `required_version`) is not affected; one that does not gets 1.13.1, and a reviewed plan saved with another version is refused and must be planned again (the existing "tofu version change" message).
- Maintainers: `tests/plan-store.test.sh` no longer fails by chance (about one run in 256): the tamper check now always changes the byte instead of writing a fixed value.
- **Docs:** "Apply before merge: the window" in `docs/runbooks.md`: between `/tofu apply` and the merge the state is ahead of the default branch, other pull requests' plans show a spurious undo, and on an `auto_apply: always` workspace another merge can destroy what was applied. The safe order is written down.
- **Changed:** the actions tfpls uses run on Node.js 24, so GitHub's "Node.js 20 is deprecated" warning is gone: `actions/checkout` v5.1.0, `actions/upload-artifact` v6.0.0, `actions/upload-pages-artifact` v5.0.0, `actions/deploy-pages` v5.0.1 (all pinned by SHA). **Self-hosted runners must be v2.327.1 or newer.**
- **Docs:** what a plan contains: the state against the merged result, so `/tofu apply` also applies changes that are on the default branch but were never applied (`never`, or what `additive_only` leaves to a person). New section in `docs/reviewed-plans.md`, a runbook entry and a link from the `auto_apply` reference.
- **Docs:** the read-only plan role needs more than `Describe*` for some resources (example: `ec2:GetManagedPrefixListEntries` for managed prefix lists); without it every plan of a workspace holding one fails with a 403 on refresh. New section in `docs/credentials.md` and a runbook entry.
- **Fixed:** a pull request plan could also plan workspaces the pull request did not change when the base branch moved on right after the pull request was opened (the diff included what the base branch gained). detect-workspaces has a new optional input `head_ref`, and `plan.yml` passes the pull request's own head, so only the pull request's own changes select workspaces.
- **Changed:** plan and apply comments no longer start with the line `⚠️ N resource(s) will be destroyed or replaced: ...`. What is destroyed or replaced stays in the `Plan: N to add, N to change, N to destroy` line and in the plan itself. The `banner` mode of `actions/tofu/summarize.sh` and `render.jq` are removed.
- **Changed:** the apply after a merge now comments its result on the merged pull request (applied, no changes, policy failure, error, or why it was not applied). The `apply.yml` job asks for `pull-requests: write`, which the caller written by `tools/onboard` already grants. A direct push posts nothing.

## v0.3.0

- **Removed (breaking in 0.x):** `create_workspace` and `tools/onboard --create-workspaces`. tfpls never creates a tofu workspace: a missing one fails the plan with a message that says to create it once with `tofu workspace new <name>`, and a config that still sets `create_workspace` fails validation. Delete the key from `.tfpls.yml`.

## v0.2.0

- Maintainers: the issue agent workflows can be run by hand from any branch (`gh workflow run claude-elaborate.yml --ref <branch> -f issue=<n>`), so a change to the agent is tested before it is merged. See CONTRIBUTING.md.
- The dashboard's "Recent activity" feed (page, `tfpls-dashboard` artifact and run summary) shows only audit events from the last 24 hours before the build, at most `events` of them (default 50), instead of the newest 50 whatever their age. The "Last apply" column is unchanged.
- Maintainers: the issue agent. Issues of the owner are elaborated automatically (questions in a comment, labels `needs-info`/`ready`), and `/implement` on a `ready` issue opens a pull request for review, with run caps, a kill switch and a cost report per run. See CONTRIBUTING.md.
- `tools/release.sh` accepts a pre-release tag `vX.Y.Z-rc.N`: it pins the references to that exact tag and pushes only it; the major tag `v0` does not move. Maintainers test a batch on a pre-release and cut a real release once per batch; see docs/releasing.md.

## v0.1.7

- The dashboard no longer has the "Last 30 days" block (applies, success rate, durations, locks, busiest, possibly unused workspaces). The workspace table and the recent activity feed stay.

## v0.1.6

- The issue of a drifted workspace shows the plan (the `Plan:` summary line and the plan in a collapsed section, like a plan comment) instead of a run link and how-to text. The tofu action has a new output `plan_text`; the drift workflow passes it to the drift report.

## v0.1.5

- A superseded plan comment no longer ends with the `/tofu apply <workspace>` hint; only the newest plan comment offers it.

## v0.1.4

- The comment of a successful apply now shows the apply output (resource actions, `Apply complete!`, first line `**Applied:** N added, N changed, N destroyed.`) instead of repeating the plan. The `Outputs:` block is never posted to the comment.

## v0.1.3

- The comment of an apply that uses a reviewed plan no longer starts with the `🔒 Applying the reviewed plan …` line; the run log and the job summary still name the plan.
- New input `allow_unencrypted_plans` (default `false`): on a private repository without `PLAN_ENCRYPTION_KEY` the saved plan is stored unencrypted without the warning. Public repositories still never save a plan without a key.

## v0.1.2

- **A new plan comment on every run.** Each plan run and each apply posts a new comment for its workspace instead of editing the first one, so it is clear which plan sits under which command. The older comments of that workspace keep their text and get a `Superseded by <link>` banner. The `Outdated` banner is only added to the newest comment of a workspace.

## v0.1.1

- Plan comments no longer repeat the apply hint above the plan (the `📦 Reviewed plan saved` line); the single `/tofu apply` hint under the plan stays.

## v0.1.0

First release as tfpls: reusable GitHub Actions workflows and composite actions for OpenTofu on AWS with S3 state. Source-available (FSL), runs on your Actions minutes.

- **One entry workflow** (`tofu.yml`, twelve inputs) routes pull requests, comments, pushes, the schedule and manual tasks; `tools/onboard` detects the layout (`workspaces/<name>/`, a var file per workspace, `envs/<name>` root modules) and writes the caller, plus a config when the layout needs one; `--solo` for working alone.
- **Plan on pull requests**: one comment per changed workspace showing only what changes, policy checks, a saved (optionally encrypted) reviewed plan. The pull request that introduces the pipeline plans everything and says so; a refused cloud role is explained once on the pull request.
- **Apply** by `/tofu apply` on an approved pull request or on merge per workspace (`auto_apply: never | always | additive_only`, `require_reviewed_plan`). Approval rules are read from the default branch; only reviews by people with write access count.
- **Drift**: scheduled checks with one issue per drifted workspace, optional remediation, a fix on demand from the drift issue or a manual task (`drift_fix` per workspace).
- **State locks**: S3 native locking, `/tofu unlock`, and a `locks` task that shows who holds each lock and releases the ones you name. The pipeline never force-unlocks by itself.
- **Safety**: strict approvals by default, CODEOWNERS approvals, fork guard, saved plans only from this repository's own runs, optional apply queue and environment gate, least-privilege job permissions, `tofu` without the GitHub token.
- **Credentials**: one IAM role through GitHub OIDC (`TFPLS_ROLE_ARN`) and/or one JSON secret (`TFPLS_ENV_JSON`); an optional read-only plan role plus apply role is documented in docs/credentials.md.
- **Visibility**: audit log and badges on the `tfpls-data` branch, dashboard, Slack/Teams/generic notifications with custom templates, runner-minute measurements.
- **Runners**: GitHub-hosted by default (`ubuntu-latest`; `ubuntu-slim` jobs are cut after 15 minutes), self-hosted supported.
