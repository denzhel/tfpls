# Changelog

All notable changes to tfpls. Versions follow SemVer; consumers pin with `@v0` or an exact tag (see docs/releasing.md).

Versions below 1.0 (0.x): the interface may still change between minor versions; read the notes before updating.

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
