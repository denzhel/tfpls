# Self-hosted runners

tfpls runs on GitHub-hosted runners or on your own. Self-hosted runners are the usual choice for state backends that are not reachable from the internet, and they avoid Actions minutes. This page covers what changes when you run the jobs yourself.

> Verified earlier on native macOS (arm64) runners, four of them on one machine, registered to one repository, in a test repository that no longer exists. Everything marked *not exercised* below is taken from GitHub's documented behavior and has not been run here.

## Why hygiene matters

A self-hosted runner keeps its working directory between jobs (`_work/<repo>/<repo>`). A plan run leaves files there that can hold secret values: the saved plan (`tfplan`), `plan.json`, plan and apply output, and the `.terraform` directory with provider binaries (hundreds of MB). `actions/checkout` removes untracked files at the start of the next job *of the same repository*, but until then they sit on disk, readable by whoever can use the machine.

tfpls therefore removes these files itself when `tofu` and `/tofu unlock` finish: the scripts clean up on exit (also on cancellation), and a final always-run step in the `tofu` and `unlock` actions covers the case where the script was killed. Repository files, including a committed `.terraform.lock.hcl`, are never touched. Measured on test runners before the change: each held `tfplan`, `plan.txt`, `.terraform` (about 660 MB per runner) after its last job.

Also remember:

- Credentials are passed to jobs as environment variables and masked in logs; they are not written to disk by tfpls. The `PLAN_ENCRYPTION_KEY` and credential secrets are only available to the jobs of the repository that holds them.
- Only point self-hosted runners at **private** repositories you trust. Anyone who can open a pull request can run code on the runner (the plan runs pull-request code, see `docs/credentials.md`), and fork pull requests must never run on a self-hosted runner.
- Give the runner its own unprivileged user, keep it patched, and give it only the network access the state backend and providers need.
- Use the labels you need in `runs_on` (`'["self-hosted","dedicated-terraform"]'`), so that unrelated workflows cannot land on these machines.

## Ephemeral and just-in-time (JIT) runners

An *ephemeral* runner takes exactly one job and then removes itself, so nothing survives between jobs, which is the strongest form of hygiene. Create one by registering with `--ephemeral`, or use the REST API for a **JIT** configuration (the runner registers itself and is deleted after one job):

```bash
# register a single-job runner (a supervisor must start a new one for every job)
./config.sh --url https://github.com/OWNER/REPO --token <registration token> --ephemeral --unattended
./run.sh

# or JIT: ask GitHub for a one-time configuration, then start the runner with it
gh api -X POST repos/OWNER/REPO/actions/runners/generate-jitconfig \
  -f name=tfpls-jit-1 -F runner_group_id=1 -f 'labels[]=self-hosted' -f 'labels[]=tofu' -f work_folder=_work \
  --jq .encoded_jit_config
./run.sh --jitconfig <the value above>
```

*Not exercised here.* Running ephemeral runners at scale needs something that starts a fresh runner per queued job: GitHub's `actions-runner-controller` on Kubernetes is the maintained option. For a handful of long-lived runners, the cleanup built into tfpls is the practical middle ground.

## Runners in containers

Run the runner itself in a container when you want isolation from the host:

- Use a **glibc-based image** (Debian or Ubuntu) for the runner. GitHub publishes its runner for Linux (x64, arm, arm64), macOS and Windows built for glibc; it does not publish a musl (Alpine) build, so Alpine is not a supported *runner host* image.
- The actions tfpls uses (`actions/checkout`, `actions/upload-artifact`, the Pages actions) run on Node.js 24, which needs a runner of at least **v2.327.1**. GitHub-hosted runners are current; update self-hosted runners before upgrading tfpls.
- An Alpine image can be used as the **job container** for a workflow (`container:`) when it has Node.js available for JavaScript actions, but this was not tried with tfpls.
- Mount nothing from the host that the jobs should not see. Pass credentials as secrets, not as mounted files.
- *Not exercised here:* a containerized runner. Local development of this repository used `act` with an Alpine-based image to run the workflows quickly; that is a test harness, not a supported runner setup.

## Running several runners on one machine

Each runner needs its own directory (its own `_work` and registration). The test setup used four, started with `caffeinate -ims ./run.sh` so that the machine does not sleep while a runner is alive (a sleeping laptop makes GitHub fail the job after losing the runner's heartbeat). Tools are cached per runner in its tool cache (`_work/_tool`), so each runner downloads OpenTofu and conftest once.

Jobs for the same workspace are serialized by the workflows' concurrency group no matter how many runners exist; runners only add parallelism across workspaces.

## This repository's own CI

The CI of tfpls itself (`.github/workflows/ci.yml`) runs on GitHub-hosted `ubuntu-latest` runners: it installs a pinned, checksum-verified `actionlint` and the shared `setup` action for conftest, and uses `shellcheck`, `yq`, `jq` and `python3` from the runner image. The pipelines of a consuming repository run on `ubuntu-latest` by default. `ubuntu-slim` works too, but GitHub cuts every job on it after 15 minutes ([changelog, 2026-01-22](https://github.blog/changelog/2026-01-22-1-vcpu-linux-runner-now-generally-available-in-github-actions/)), which would kill a long apply half-way: use it only when plans and applies finish well inside that, and keep `timeout_minutes` below 15; the self-hosted runners described above are an option for users, not something this project runs.
