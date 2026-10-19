# Runner minutes and cost guide

On **self-hosted runners** none of this costs anything but the machine. On **GitHub-hosted** runners every job is billed per started minute (rounded up per job), so many short jobs are expensive relative to their runtime. Free private repositories include 2,000 Linux minutes a month; check GitHub's current pricing for your plan.

## Observed durations

Measured earlier in a test repository (localstack, one small workspace per job, self-hosted macOS runner, tools cached):

| Job | Typical time | Billed on hosted |
|---|---|---|
| PR plan, one workspace | ~30 s | 1 min |
| Merge apply, one workspace | ~40 s | 1 min |
| `/tofu` command | ~30 s | 1 min |
| `locks` task, one job for all workspaces | ~6 s + ~20 s per workspace | 1 min or more |
| Dashboard build | ~15 s | 1 min |

Add ~15 to 30 s on hosted runners for the first-time tool download (no persistent tool cache) and real cloud API latency for large states: a big workspace can take minutes to refresh, which is what you are paying for.

### Real AWS on GitHub-hosted runners

Measured in a test repository on `ubuntu-slim` (GitHub-hosted, 1 vCPU) with three workspaces in `eu-north-1`, each a VPC with two subnets, a NAT gateway and one EC2 instance (about 24 resources), an S3 state bucket with native locking and the AWS provider downloaded on every job:

| Job | Duration | Billed (rounded up per job) |
|---|---|---|
| detect / parse | 6-11 s | 1 min |
| PR plan, one workspace | 32-45 s (first run with a cold provider download up to 90 s) | 1-2 min |
| Apply of a new workspace (24 resources, NAT gateway) | 2 min 50 s to 3 min 25 s | 3-4 min |
| Merge apply with nothing to change | 50-65 s | 1 min |
| Drift check, one workspace | 37-56 s | 1 min |
| `locks` task, 3 workspaces (one job) | ~66 s | 2 min |
| Dashboard build | 11 s | 1 min |

A pull request that touches three workspaces plans in about 2 minutes of wall clock (first run, cold) and bills about 4-5 minutes; the daily schedule (drift of three workspaces plus the dashboard) bills 5 minutes (about 150 a month). Creating a NAT gateway dominates the apply time. `ubuntu-slim` had every tool the pipelines need (`gh`, `jq`, `yq`, `git`, `curl`) and ran as fast as `ubuntu-latest` in an earlier check with local state.

An earlier check on `ubuntu-slim` and `ubuntu-latest` with local state and no provider (two workspaces) gave: PR plan 25 s wall clock, merge apply 24-30 s, drift 35 s, dashboard 15 s.

Cost of the test infrastructure itself: a NAT gateway is about $1 a day each plus data; the three workspaces were destroyed after the test (`tofu destroy`), the month-to-date cost was $0 thanks to the account credits. The test account restricts workloads to `eu-north-1` with an organization policy, which is why the workspaces are named `*-eu-north-1`.

## Budget a schedule

Billed minutes per day ≈ runs per day × jobs per run × 1 minute (each workspace is a job, plus one `detect` job per run).

| Schedule (N workspaces) | Minutes per day |
|---|---|
| Drift, daily | N + 1 |
| The `locks` task (only when you start it) | 1 + about N/3 |
| Dashboard (built after the scheduled drift run or by hand) | 1 per build |
| PR plans | (1 + changed workspaces) × pushes per PR |

Billed minutes per event (each job is billed per started minute; a job that runs over 60 s bills 2): a push to a pull request that changes one workspace 2 (detect and plan); a change to a shared file 1 + N; a merge that changes k workspaces 1 + k (workspaces with `auto_apply: never` still run a plan job that ends as skipped); `/tofu plan` or `/tofu apply` on k workspaces 1 + k; `/tofu unlock` 2; every rejected or unmatched `/tofu` comment 1; every push that changes only ignored files 1 (the detect job); the daily schedule N + 2. For N = 3 / 10 / 30 workspaces that is about 150 / 360 / 960 minutes a month for the schedule alone.

Example, 20 workspaces: the daily schedule is 22 minutes (detect, 20 drift jobs, the dashboard), about 660 a month. The default schedule (`tools/onboard`) is one daily cron that runs drift and then the dashboard: N + 2 minutes a day.

## Recommendations

- **Self-hosted for the schedules** (drift, locks, dashboard), hosted only for what needs it.
- Nothing releases locks on a schedule, so no minutes go there; run the `locks` task when a plan reports a lock ([state locks](locks.md)).
- Keep `workspace_paths` accurate so a change selects one workspace, not all of them.
- Use `max_parallel` to bound concurrency, and `timeout_minutes` so a hung job cannot burn an hour.
- `queue: true` jobs wait while holding a runner: prefer the default concurrency group on hosted runners.

## The `ubuntu-latest` label moves to Ubuntu 26.04

GitHub moves `ubuntu-latest` from Ubuntu 24.04 to Ubuntu 26.04 between 2026-10-19 and 2026-11-19 ([announcement](https://github.com/actions/runner-images/issues/14748)). Callers with `runs_on: ubuntu-latest` (the default) switch on their own during that time. What differs on the image, from the published software lists: jq 1.7 to 1.8.1, Bash 5.2 to 5.3, OpenSSL 3.0 to 3.5, Python 3.12 to 3.14, curl 8.5 to 8.18, Node.js 22 to 24; yq, git and the GitHub CLI are the same.

What was checked on the explicit label `ubuntu-26.04`: the CI of tfpls (the whole offline suite passes; the `lint` job passes once actionlint accepts the label, because actionlint 1.7.12 does not know it yet and says `label "ubuntu-26.04" is unknown`), and a full cycle in a test repository with `runs_on: ubuntu-26.04`: a pull request plan, `/tofu apply`, the merge apply and the dashboard all ran green.

To stay on the old image until you have tried the new one, set `runs_on: ubuntu-24.04`; to move early, set `runs_on: ubuntu-26.04` (and expect an older actionlint to complain about the label). Self-hosted runners are not affected.
