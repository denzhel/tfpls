# Changelog

All notable changes to tfpls. Versions follow SemVer; consumers pin with `@v0` or an exact tag (see docs/releasing.md).

Versions below 1.0 (0.x): the interface may still change between minor versions; read the notes before updating.

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
