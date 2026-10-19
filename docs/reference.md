# Reference

Every setting, command and input. For a first setup read [getting started](getting-started.md); the [README](../README.md) has the overview.

## Config layouts

`.tfpls.yml` is not needed for the `workspaces/<name>/` layout (a top-level `overrides:` holds just the exceptions). For anything else describe the stacks (`tools/onboard` writes the common ones, see [getting started](getting-started.md#another-layout)):

```yaml
defaults:
  auto_apply: never            # never | always | additive_only
stacks:
  - name: terraform
    dir: .                     # the root module
    workspaces_glob: "workspaces/*"      # or: workspaces_file: path/to/list, or: workspaces: [a, b]
    workspace_paths: ["workspaces/{workspace}/"]   # changes here select only that workspace
    ignore: ["**/*.md", ".github/**", "docs/**"]   # never trigger anything
    watch: []                  # extra paths outside `dir` that count as shared
    var_file: "vars/{workspace}.tfvars"             # optional, relative to dir
    parallelism: 10
    policy_dir: policies       # optional conftest/Rego dir
    overrides:                 # first-class per-workspace settings, glob keys
      "dev-*":  { auto_apply: always }
      "stg-*":  { auto_apply: additive_only }
      "prod-*": { auto_apply: never }
```

Selection rules: any changed file under `dir` (minus `ignore`) selects **every** workspace, except files under a `workspace_paths` prefix, which select only their own workspace. If `workspace_paths` is unset, any change selects all workspaces (the safe default).

Runners: pass `runs_on` (`with: { runs_on: self-hosted }` in the caller), either a label or a JSON array of labels (`'["self-hosted","dedicated-terraform"]'`). Credentials and the optional secrets (`PLAN_ENCRYPTION_KEY`, `SLACK_WEBHOOK`): [credentials.md](credentials.md).

Job titles, PR comment headings and Slack messages use just the workspace name (`prod-us-west-1`). If two stacks share a workspace name the label falls back to `stack/workspace`.

## Config reference

| Key | Default | Meaning |
|---|---|---|
| `name` | required | Stack name (also the id prefix: `stack/workspace`) |
| `dir` | `.` | Root module directory |
| `workspaces` / `workspaces_file` / `workspaces_glob` | none | Workspace list: inline, one name per line in a file (`#` comments ok), or directory names matching a glob |
| `workspace_paths` | none | Prefix templates owned by one workspace, e.g. `workspaces/{workspace}/` |
| `triggers` | none | Map of workspace glob to a list of workspace globs: when a change selects a matching workspace, the listed ones are planned too (transitive), e.g. `"net-*": ["app-*"]`. Applies to change-based selection only, not to explicit workspace names; `ignore` does not filter triggered workspaces. |
| `ignore` | none | Globs (`*`, `?`) of files that never trigger anything |
| `watch` | none | Extra paths treated as shared changes |
| `auto_apply` | `never` | After merge: `never`, `always`, or `additive_only` |
| `plan_lock` | `false` | `false` = plans that will never be applied (PR plans, `/tofu plan`, drift reports) run with `-lock=false`, so they never block an apply; `true` = they take the state lock |
| `lock_timeout` | `2m` | how long plans and applies wait for a held state lock (`-lock-timeout`), e.g. `30s`, `5m` |
| `var_file` | none | `-var-file`, `{workspace}` is substituted |
| `parallelism` | `10` | `-parallelism` for plan |
| `policy_dir` | none | Rego directory (conftest, input = plan JSON) |
| `policy_params` | none | Parameters of the packs, e.g. `safety: { max_destroys: 3 }`, see [policies.md](policies.md#parameters-policy_params) |
| `require_reviewed_plan` | `false` | only matters with `auto_apply: always` or `additive_only`: `true` = the apply after a merge refuses when no saved reviewed plan can be used, instead of re-planning. `/tofu apply` always needs a reviewed plan, see [reviewed-plans.md](reviewed-plans.md) |
| `drift_fix` | `true` | `false` = the drift of this workspace cannot be fixed on demand from its issue or the `fix` task, only through a pull request, see [drift.md](drift.md) |
| `policy_packs` | none | Packs from the shared [policy library](policies.md), e.g. `[safety, aws-s3]`; evaluated together with `policy_dir` |
| `after` | none | List of workspace globs that must be applied successfully first (only those in the same run are waited for), see [ordering.md](ordering.md) |
| `drift_remediate` | `never` | `never` / `additive_only` / `always`: fix drift automatically (off unless you set it) |
| `notify_secret` / `notify_format` / `notify_mention` | `SLACK_WEBHOOK` / `slack` / none | Per-workspace webhook secret name, `slack` / `teams` / `generic`, mention on failures, see [notifications.md](notifications.md) |
| `notify_template` | built-in Slack message | Path of a jq file that builds the webhook body from the run JSON (empty output sends nothing), see [notifications.md](notifications.md#your-own-message-notify_template) |
| `overrides` | none | Map of workspace glob to any of the settings above |

`additive_only` auto-applies only if every resource change is `create`, `no-op` or `read`. Updates, deletes and replacements require a human; until one applies them they stay in the plan of every later pull request of that workspace ([what a plan contains](reviewed-plans.md#what-a-plan-contains)).

## Removed settings

`approvals`, the `require_codeowners` input and the `badges` input were removed in v0.9.0: the number of approvals and the CODEOWNERS requirement come from your repository's ruleset ([approvals.md](approvals.md)), and the badges feature is gone. A config that still has `approvals:` is an error that says so.

`create_workspace` and `tools/onboard --create-workspaces` were removed: tfpls never creates a tofu workspace. Create each one once with `tofu workspace new <name>`; a config that still has `create_workspace` is an error that says so.

## PR commands

```
/tofu plan                                  # workspaces changed by the PR
/tofu plan prod-us-west-1 dr-us-east-1      # explicit workspaces
/tofu plan prod-*                           # globs; `all` for everything
/tofu apply dev-us-east-1 stg-us-west-2
/tofu apply prod-us-west-1 -target=module.vpc   # exactly one workspace with -target (bootstrap phases)
/tofu apply prod-us-west-1 --now            # repository admins only: skip the required approvals
/tofu unlock prod-us-west-1                 # release a stuck state lock (finds the ID itself)
```

On a `Drift: <workspace>` issue (not a pull request) the same commands `/tofu plan`, `/tofu apply` and `/tofu fix` fix that workspace from the default branch, see [drift.md](drift.md#fixing-a-drift-on-demand-no-pull-request).

`/tofu unlock` runs the default branch's code. It probes the state for its lock, then `force-unlock`s exactly that ID. It refuses (and says why) if the lock is younger than 5 minutes, if a pipeline job for that workspace is still queued or running, or if it cannot check (the caller workflow needs `actions: read`). It replies with the lock ID, holder, operation and time it released. The pipeline never releases a lock by itself: the manual task `locks` shows who holds each lock and releases the ones you name ([locks.md](locks.md)).

`/tofu apply` needs a reviewed plan for the PR's current commit (made automatically by the PR plan, or by `/tofu plan`).

Guards: commenter needs write access; same-repo PRs only (forks never get credentials); apply requires approving review(s) and a branch up to date with base (`require_up_to_date`). The number of approvals and how strict they are come from the pull request rule of your default-branch ruleset ([approvals.md](approvals.md)); a repository admin can skip them with `/tofu apply <workspace> --now`. Targets are validated against a strict character allowlist. Apply always runs the saved plan that was policy-checked.

## Policies

Plain conftest. Input is the plan JSON, package `main`, `deny` rules fail the check:

```rego
package main

import rego.v1

deny contains msg if {
	some rc in input.resource_changes
	"delete" in rc.change.actions
	startswith(rc.type, "cloudflare_zone")
	msg := sprintf("%s: zones must not be destroyed", [rc.address])
}
```

See [`policies/example.rego`](../policies/example.rego).

## Concurrency: what happens when several things hit one workspace

Runs that change a workspace (`/tofu plan|apply` comments, applies after a merge, drift runs) share one GitHub `concurrency` group per workspace, so they are **serialized**. GitHub keeps one running run and **one pending** run per group: if a third arrives while one is running and one is pending, **the pending one is cancelled** and leaves no message on its pull request. Set the input `queue: true` to get a first-come-first-served queue instead: nothing is cancelled, waiting runs say their position on the pull request ([queue.md](queue.md)). What to expect without the queue when three PRs post `/tofu apply <workspace>` at the same moment:

| Run | Outcome |
|---|---|
| first | applies its reviewed plan |
| second (waits its turn) | refused: "the state changed after the plan was made", plan again and review |
| third (displaced while pending) | cancelled by GitHub, no PR comment; post the command again |

Reviewed plans are single-use and bound to the state they were planned against, so a later apply can never overwrite a change it has not seen. PR plans of the same workspace from different PRs are not serialized with each other (their group is per PR); they only hold the state lock briefly. `/tofu unlock` is not in the workspace group on purpose, so it never queues behind the run it is unblocking, and it refuses while a run is active on that workspace or while the lock is younger than 5 minutes.

A `/tofu` comment that cannot be run (unknown subcommand, unknown workspace, illegal characters, `-target` with several workspaces, apply without approval, ...) gets a reply on the pull request with the reason and a 😕 reaction.

## Pull requests from forks

GitHub withholds secrets from workflows that run for a pull request from a fork, so a plan could only fail in `init`; on private repositories fork pull-request workflows do not run at all unless the repository setting "Run workflows from fork pull requests" is on. The pipeline therefore **does not plan fork pull requests**: `plan.yml` detects them, skips the plan jobs and says why in a notice and in the job summary (the read-only token of a fork run cannot post a comment), and `/tofu` commands on a fork pull request are refused with a reply. A maintainer who wants a fork's change planned pushes the branch to this repository and opens the pull request from there.

There is deliberately **no option to plan forks with credentials** (the `pull_request_target` pattern). That trigger runs with secrets and a write token, and running a contributor's code there is exactly how credentials get stolen, since plans execute pull-request code. Never run fork pull requests on a self-hosted runner either ([runners.md](runners.md)).

## Config checks

`.tfpls.yml` is validated on every run. A wrong value or structure (for example `plan_lock: "false"` in quotes, `lock_timeout: 5`, a stack without a `name`, an `overrides` entry that is not a map) stops the run with a message that names the path, such as `stacks[0].overrides["prod-*"].auto_apply: must be never, always or additive_only`. An unknown key is only a warning, because it is ignored and is usually a typo: ``stacks[0]: unknown key `autoapply` is ignored (did you mean `auto_apply`?)``.

## Workflow inputs

Everything the caller can set, with `with:` on the `tofu.yml` job. The default `runs_on` and `tofu_version` need no change for most repositories; the credentials are the repository variable `TFPLS_ROLE_ARN` and the secrets (see [credentials.md](credentials.md)), not inputs.

| Input | Default | Meaning |
|---|---|---|
| `runs_on` | `ubuntu-latest` | runner label, or a JSON array of labels |
| `config` | `.tfpls.yml` | path of the config file |
| `tofu_version` | detected | OpenTofu version: by default read from `.opentofu-version`, `.tofu-version`, `.terraform-version` or an exact `required_version`, else 1.10.6 |
| `apply_environment` | none | GitHub Environment used by apply-capable jobs (apply, unlock, drift remediation, the `locks` task) |
| `audit` | `true` | append an [audit event](audit.md) per apply, drift check and unlock to the `tfpls-data` branch (needs `contents: write`) |
| `drift_issues` | `true` | open and close one issue per drifted workspace, see [drift.md](drift.md) (alerts are sent on transitions either way) |
| `queue` | `false` | first-come-first-served apply queue per workspace, nothing is cancelled, see [queue.md](queue.md) |
| `require_up_to_date` | `true` | `/tofu apply` refuses a pull request that is behind its base branch |
| `max_parallel` | `4` | workspaces applied, drift-checked or fixed at the same time (pull request plans run up to 20) |
| `timeout_minutes` | `60` | time limit of the jobs that run tofu |
| `allow_unencrypted_plans` | `false` | `true` = on a **private** repository without `PLAN_ENCRYPTION_KEY`, saved plans are stored unencrypted without the warning; public repositories still never save a plan without a key ([reviewed-plans.md](reviewed-plans.md#why-encrypt)) |

The manual run offers the tasks `drift`, `locks` (`workspaces` = locks to release by name, see [locks.md](locks.md)), `dashboard` and `fix`. The dashboard publishes to the data branch; GitHub Pages is an option of `dashboard.yml` ([dashboard.md](dashboard.md)).

GitHub allows at most 256 jobs in one matrix. A change that selects more workspaces fails early with a message that says how many were selected; split the change or plan in batches (`/tofu plan <glob>`).

## Plan comments

The apply that runs after a merge comments on the merged pull request too (found through the merge commit; a direct push to the default branch has no pull request and posts nothing): an applied workspace shows what was applied, a workspace that is not auto-applied (`auto_apply: never`, or a change `additive_only` leaves to a person) shows why it was not applied. This needs `pull-requests: write` in the caller workflow, which `tools/onboard` writes.

Every plan run and every apply posts a **new** comment for its workspace; the newest one is the live one. The older comments of that workspace keep their text and get a banner under the first line: `> 🔁 **Superseded** by <link to the newer comment>. The plan below is kept for reference only.` Only the newest plan comment ends with the `/tofu apply <workspace>` hint; a superseded comment loses it together with getting the banner. The comment of a successful apply shows what the apply did (the resource actions and `Apply complete!`, first line `**Applied:** N added, N changed, N destroyed.`), not the plan again; the `Outputs:` block is never posted to a comment. A plan comment is laid out like this:

````markdown
**Plan:** 1 to add, 1 to change, 0 to destroy.
### 📝 [dev](https://github.com/OWNER/REPO/actions/runs/123) plan logs

<details><summary>Click here to expand</summary>

```terraform
  # aws_sqs_queue.this["jobs"] will be updated in-place
  ~ resource "aws_sqs_queue" "this" {
      ~ message_retention_seconds = 1209600 -> 1123200
    }
```
</details>

`/tofu apply dev` to apply
````

- The first line is OpenTofu's own `Plan:` line. The heading links to the run and says `apply logs` after an apply; a clean run says `**No changes.**` and has nothing to expand, a failed one shows the error text and a hint instead of a plan.
- The collapsed plan is `tofu show` of the saved plan: only what changes (updated resources list the changed attributes, `# (N unchanged attributes hidden)`), no `Refreshing state...` lines. **Sensitive values are never shown** (`(sensitive)`), unknown values appear as `(known after apply)`.
- Policy and reviewed-plan notices stay above the collapsed plan; what the change destroys or replaces is in the `Plan:` line and the plan itself.
- Under the marker line the comment has a hidden `<!-- tfpls-status: planned -->` line (`planned`, `applied`, `no-changes`, `error`, ...), for tools that read the comments; the pipelines themselves do not depend on it.

A plan that is too long for a pull request comment (GitHub's limit is 65,536 characters) is clipped in the collapsed section to its beginning and its end, where the `Plan: N to add...` summary is, with a note; the full text is in the run logs. When a later commit of the PR no longer changes a workspace (computed against the PR's base branch, so for example the change was reverted or a shared file that selected everything was removed), its newest comment gets an **Outdated** banner (the old plan stays below it for reference), so a stale plan is never mistaken for a current one; planning that workspace again posts a new comment without the banner.

## State locks while planning

Plans and applies wait up to `lock_timeout` (default `2m`) for a held state lock, then fail. Plans that will never be applied (pull request plans, `/tofu plan`, drift reports) run with `-lock=false` by default (`plan_lock: false`), so they can never block an apply, and a cancelled plan can never leave a lock behind. **Trade-off:** such a plan may read the state in the middle of an apply and show a slightly stale picture; `/tofu apply` applies the saved plan, which tofu rejects if the state moved after it was made. Set `plan_lock: true` (per stack or override) if you want plans to take the lock too. Plans that may be applied (the apply after a merge, drift with remediation) always keep the lock.

A lock left behind by a cancelled or timed-out job is released with `/tofu unlock`.

## Timeouts

Every job has a time limit so a hung runner or `tofu` cannot run for hours. The jobs that run `tofu` use the workflow input `timeout_minutes` (default 60, on `plan.yml`, `apply.yml`, `drift.yml` and `comment-ops.yml`); detect/parse jobs are limited to 10 minutes and `/tofu unlock` to 15. A timed-out job is cancelled by GitHub and the run turns red. GitHub stops the step without letting `tofu` clean up, so the **state lock can stay behind**: release it with `/tofu unlock <workspace>` (it refuses locks younger than 5 minutes).

## Required status check (optional)

Branch protection cannot require the per-workspace jobs (their names change with every PR). If you want one stable check, add this job to your **caller** workflow; it adds a row only where you add it:

```yaml
  plan-result:
    needs: tofu                    # the name of the job that calls tofu.yml
    if: always() && github.event_name == 'pull_request'
    runs-on: ubuntu-latest
    steps:
      - uses: denzhel/tfpls/actions/result@v0
        with: { needs: "${{ toJSON(needs) }}" }
```

It is green when every workspace plan succeeded or the PR touched no workspace, and red when any plan failed or was cancelled. Require `plan-result` in the branch protection rules.

## Reviewed plans

`/tofu apply` applies the plan that was reviewed on the PR, not a fresh one, and refuses when the code or state moved. Plans are saved as artifacts, encrypted when you set a `PLAN_ENCRYPTION_KEY` secret. See [reviewed-plans.md](reviewed-plans.md) for setup, key creation, debugging and decrypting a plan.

## State backends

**S3 state with native lockfile locking (`use_lockfile`) is the supported backend;** there is no DynamoDB lock table support. S3-compatible services (Cloudflare R2) come last. LocalStack and SeaweedFS are verified on GitHub-hosted runners; real AWS S3 with OIDC is verified end to end; R2 is not. See [backends.md](backends.md) for configuration, what was verified, and how locks behave after a killed job.

## Self-hosted runners

Plan and apply files are removed from the runner's workspace when each job ends, and `runs_on` accepts a label or a JSON array of labels. See [runners.md](runners.md) for hygiene, ephemeral/JIT runners and containers.

## Tool installation

The `setup` action installs OpenTofu and conftest directly from their GitHub releases: each download is retried with backoff, the archive is verified against the SHA-256 in the release's checksum file, and tools are cached in the runner's tool cache, so a self-hosted runner downloads each version once. A transient network error no longer fails a run.

## Development

```bash
bash tests/run-all.sh                                   # offline test suite (detection, -target allowlist, unlock parsing, credentials, saved plans)
actionlint                                              # lint workflows and the scripts inside them
shellcheck -S warning actions/*/*.sh tools/* tests/*.sh
```

CI (`.github/workflows/ci.yml`) runs the same on every pull request and on `main`: actionlint, shellcheck, YAML parse and the offline tests on a GitHub-hosted runner. Workflow changes are additionally verified in a test repository on real AWS (see `AGENTS.md`).

**Requiring CI before merge** (active on this repository; it needs a public repository or GitHub Pro, a Free private repository answers 403): a ruleset on the default branch that requires the `lint` and `tests` checks and blocks force pushes and deletion. Checked: a pull request with red checks cannot be merged, and a direct push to `main` is refused.

```bash
gh api -X POST repos/OWNER/REPO/rulesets --input - <<'JSON'
{ "name": "main", "target": "branch", "enforcement": "active",
  "conditions": { "ref_name": { "include": ["~DEFAULT_BRANCH"], "exclude": [] } },
  "rules": [ { "type": "deletion" }, { "type": "non_fast_forward" },
             { "type": "required_status_checks", "parameters": { "strict_required_status_checks_policy": false,
               "required_status_checks": [ { "context": "lint" }, { "context": "tests" } ] } } ] }
JSON
```
