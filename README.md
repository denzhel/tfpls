# tfpls

OpenTofu on GitHub Actions: a plan on every pull request, `/tofu apply` by comment, apply on merge, drift checks, state-lock tools and an audit log. It is a set of reusable workflows that run in your repository on your Actions minutes; there is nothing to host and no server of ours in the path. Source-available under the [FSL](LICENSE); it works with **AWS** (OIDC) and **S3 state** with native locking. 0.x: the interface can still change between minor versions ([changelog](CHANGELOG.md)).

## What it looks like

A pull request that changes `workspaces/dev` gets a new comment on every plan run (the older ones are marked as superseded):

````markdown
**Plan:** 1 to add, 1 to change, 0 to destroy.
### 📝 dev plan logs

> ⚠️ **1 resource(s) will be destroyed or replaced:** `aws_s3_bucket.data` (replace)

<details><summary>Click here to expand</summary> … only what changes, sensitive values hidden … </details>

`/tofu apply dev` to apply
````

`/tofu apply dev` applies exactly that saved plan, and only when the rules are met. Otherwise the answer is a comment and a 😕, never a silent skip:

> ⛔ **apply refused.** `dev`: apply needs 1 approving review(s) of the current commit (3f9a1c2); found 0  _(mode `strict`, 1 required)_. The number of approvals comes from the repository's ruleset for `main` (Settings, Rules; 1 when there is none). In an emergency a repository admin can comment `/tofu apply <workspace> --now`.

## Quick start

For a repository with one root module and a folder per tofu workspace (`workspaces/dev`, `workspaces/prod`); `tools/onboard` also handles var-file and `envs/<name>` layouts.

1. **Onboard** (reads your files, writes `.github/workflows/tofu.yml`, no network):
   ```sh
   curl -fsSL https://raw.githubusercontent.com/denzhel/tfpls/v0/tools/onboard | bash
   ```
   Pass flags as `| bash -s -- --create-workspaces`; use it if the tofu workspaces do not exist in your state yet (first run only).
2. **One IAM role** that GitHub can assume through OIDC (the onboarding prints the trust policy for your repository), then `gh variable set TFPLS_ROLE_ARN --body arn:aws:iam::<account>:role/<role>`.
3. **Encrypt saved plans**: `openssl rand -base64 32 | gh secret set PLAN_ENCRYPTION_KEY` (required on public repositories).
4. **Commit the files and open ONE pull request.** It plans every workspace and says so. Merge it (nothing is applied); from the next pull request, comment `/tofu apply <workspace>` after a review.

Details, other layouts, secrets instead of OIDC, self-hosted runners: [getting started](docs/getting-started.md). First run going wrong: the [first-run runbook](docs/runbooks.md#first-run).

## What you get

| | |
|---|---|
| Plan | on every pull request, one comment per workspace, policy checks ([conftest](docs/policies.md)) |
| Apply | `/tofu apply <workspace>` applies the reviewed plan; or on merge per workspace (`auto_apply`) |
| Review rules | approvals of the latest commit by people with write access, optional CODEOWNERS ([approvals](docs/approvals.md)) |
| Drift | scheduled; an issue per drifted workspace, fixable by comment ([drift](docs/drift.md)) |
| Locks | the manual task `locks` shows who holds each state lock and releases the ones you name ([locks](docs/locks.md)) |
| Queue, ordering | optional first-come-first-served queue, `after:` ordering ([queue](docs/queue.md), [ordering](docs/ordering.md)) |
| Records | audit log, dashboard, Slack/Teams/webhook messages ([audit](docs/audit.md), [dashboard](docs/dashboard.md), [notifications](docs/notifications.md)) |

Every setting, command and workflow input: [docs/reference.md](docs/reference.md). Runbooks for stuck locks, failed applies and drift: [docs/runbooks.md](docs/runbooks.md).

## Security model

- **The cloud credentials are the only hard boundary.** Plans run the code of the pull request, so anyone who can push a branch to the repository runs code with the plan role. Approvals, CODEOWNERS and reviewed plans are process controls on top. For a real boundary give apply its own role, kept in a GitHub Environment limited to the default branch with required reviewers and prevent-self-review, and let that role's trust policy accept only `environment:<name>` ([credentials](docs/credentials.md)).
- **Approvals**: an apply needs the approvals your repository asks for, from people with write access: the number, "current commit only", code owners and "not the last pusher" are read from the pull request rule of your default-branch ruleset (1 approval of the current commit when there is none), never from the pull request. A repository admin can apply in an emergency with `/tofu apply <workspace> --now`. The workspace rules are read from the default branch config and matched by directory and workspace. A pull request that points a new directory at existing state is code: only review catches that. Two accounts of one person look like two people. `auto_apply` and drift fixes skip approvals: protect your default branch, and set `drift_fix: false` where applies need approval.
- **Saved plans** are bound to the commit, the tree, the directory and the OpenTofu version, are single-use, and only artifacts of this repository's own runs are used. On public repositories plans are saved only encrypted; the key is readable by every workflow in the repository, so it keeps outsiders out, not writers.
- **Public repositories**: comments, logs and the data branch are public. Fork pull requests are never planned and get no credentials. Never use self-hosted runners.
- **Locks**: the pipeline never force-unlocks state by itself.
- **Least privilege**: every job asks for the token permissions it needs, and `tofu` runs without the GitHub token and the plan key.
- **Supply chain**: third-party actions are pinned by SHA and tools are verified by checksum. `@v0` moves with each release; pin an exact tag if you want it fixed.

More: [docs/security-model.md](docs/security-model.md) · report vulnerabilities as in [SECURITY.md](SECURITY.md).

## Limits

- AWS and S3 state with `use_lockfile` are what is supported and tested; other backends (R2, LocalStack) are described in [backends](docs/backends.md).
- Everything is billed to your own Actions minutes, one minute per job at least ([runner minutes](docs/runner-minutes.md)); self-hosted runners avoid that on private repositories.
- Without `queue: true` GitHub keeps one pending run per workspace, so a third concurrent run is cancelled ([reference](docs/reference.md#concurrency-what-happens-when-several-things-hit-one-workspace)).
- A matrix holds at most 256 workspaces per run.
- Not exercised: self-hosted runners at scale and backends other than S3. Try it on a throwaway workspace first.

## Development

```sh
bash tests/run-all.sh      # offline tests
actionlint
shellcheck -S warning actions/*/*.sh tools/* tests/*.sh
```

[Contributing](CONTRIBUTING.md) (sign-off under the DCO) · [Releasing](docs/releasing.md) · [Licence: FSL-1.1-ALv2](LICENSE) (use, modify and contribute freely; no competing product or service; each release becomes Apache-2.0 after two years).
