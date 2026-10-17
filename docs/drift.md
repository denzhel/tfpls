# Drift: alerts, issues and auto-remediation

`drift.yml` plans every workspace on a schedule and reports whether the real infrastructure still matches the code.

`tofu.yml` runs the drift check on the schedule of your caller (the daily cron that `tools/onboard` writes) and when you start the manual task `drift`:

```yaml
jobs:
  tofu:
    uses: denzhel/tfpls/.github/workflows/tofu.yml@v0
    with: { drift_issues: true }   # the default
    secrets: inherit
```

## Alerts on transitions, one issue per workspace

The last known state of each workspace is kept on the `tfpls-data` branch (`drift/<workspace>.json`) and only changes are announced (the state is kept whatever `drift_issues` says; the setting only decides whether an issue is opened and closed; if the state cannot be written, any non-clean result alerts):

| Was | Now | Slack | Issue `Drift: <workspace>` |
|---|---|---|---|
| in sync | drift | `drift (drifted)` | opened |
| drift | drift | quiet | body refreshed (no notification) |
| drift | in sync | `drift (recovered)` | commented and closed |
| any | check failed | `drift (failing)`, once | commented, if open |

A workspace seen for the first time counts as having been in sync, so an already drifted workspace alerts once. If the drift check itself fails repeatedly you hear about it once, not on every run.

## Auto-remediation (`drift_remediate`)

Per workspace, in `.tfpls.yml` (`defaults`, a stack or `overrides`):

```yaml
overrides:
  "dev-*": { drift_remediate: always }
  "stg-*": { drift_remediate: additive_only }   # only create what is missing; changes and deletions are reported
  "prod-*": { drift_remediate: never }          # the default
```

- `additive_only` applies a drift plan only when it contains nothing but creations (for example a deleted queue is recreated). Updates, replacements and deletions are only reported.
- `always` applies whatever the plan says.
- Remediation goes through the normal pipeline: policies must pass (`policy_dir`, `policy_packs`), state locks are respected, `apply_environment` protects the apply credentials, and every remediation is a `drift` event (via `drift-remediate`) in the [audit log](audit.md).
- Remediation uses the same credentials as every other job.

## Fixing a drift on demand (no pull request)

Every drifted workspace has an issue `Drift: <workspace>`. Its body has a small table (since when, last check, the plan summary) and the **plan** of the drift, in a collapsed section like a plan comment, refreshed on every drift check; there is no run link or how-to text in it, the ways to fix it are the ones below. Someone with **write access** can fix exactly that workspace from there, or from the Actions tab, without opening a pull request:

| Where | What |
|---|---|
| comment `/tofu plan` on the drift issue | plans the workspace from the **default branch now**, saves the reviewed plan and posts the plan comment on the issue |
| comment `/tofu apply` | applies exactly that saved plan; refused with the reason (and the command to run again) when the default branch moved or the state changed since the plan |
| comment `/tofu fix` | plan and apply in one run; the comment shows what was applied |
| Actions tab: run the workflow, task `fix`, inputs `workspaces` (names, no globs) and `step` (`plan`, `apply`, `fix`; default `fix`) | the same, for one or several workspaces; the result goes to the open drift issue of each |

Add `-target=<address>` to limit it to a resource. The issue is one workspace: other names are refused, and `/tofu` commands on any other issue get a short explanation.

It uses the same machinery as a pull request apply: the credentials of the job, the policies (`policy_dir`, `policy_packs` must pass), state locks, the saved reviewed plan (`PLAN_ENCRYPTION_KEY`), `apply_environment` for the apply job, the audit log (`via: drift-fix`), and notifications. Unlike a pull request there is no second reviewer: anyone with write access may apply (the plan comment and the audit log are the record). To forbid it for a workspace set `drift_fix: false` (default `true`):

```yaml
overrides:
  "prod-*": { drift_fix: false }    # production drift is fixed through a pull request only
```

The next drift check turns green and closes the issue. To change the code instead of the infrastructure (accept the drift), open a pull request.

