# Security model

What can reach what, and what stops it.

## What to rely on

1. **The cloud credentials are the only hard boundary.** Plans run the code of the pull request, so anyone who can push a branch to the repository runs code with the plan role. Approvals, CODEOWNERS and reviewed plans are process controls on top of that. For a real boundary give apply its own role: keep it in a GitHub Environment limited to the default branch with required reviewers and prevent-self-review, and let the role's trust policy accept only `environment:<name>` ([credentials.md](credentials.md)).
2. **Approvals.** Only approvals of the current commit by users with write access count. The rules are read from the default branch and matched by directory and workspace, never from the pull request's own config. A pull request that points a new directory at existing state is code: only review catches that. Two accounts of one person are indistinguishable from two people. `auto_apply` and drift fixes skip approvals, so protect the default branch and set `drift_fix: false` where applies need approval.
3. **Saved plans** are bound to the commit, the tree, the directory and the OpenTofu version. Only artifacts of this repository's own runs are used. On public repositories plans are saved only encrypted; the key is readable by every workflow in the repository, so it keeps outsiders out, not writers.
4. **Public repositories.** Comments, logs and the data branch are public. Fork pull requests are never planned and get no credentials. Never use self-hosted runners.
5. **Locks.** The pipeline never force-unlocks state by itself: the `locks` task shows holders, `/tofu unlock` and the task release a named lock after the checks in [locks.md](locks.md).
6. **Supply chain.** Third-party actions are pinned by SHA, tools are verified by checksum. `@v0` moves with each release; pin an exact tag to fix it.

## Who can do what

| Actor / code | Runs with | Cannot |
|---|---|---|
| **Pull request code** (from a collaborator; never from a fork) in `plan` jobs | the role (`TFPLS_ROLE_ARN`) or the credential map (`TFPLS_ENV_JSON`); `GITHUB_TOKEN` as granted by the caller | run at all from a fork (plans for fork PRs are skipped by the fork guard); anything the role's trust policy and permissions do not allow. **It can use the role**: see [credentials.md](credentials.md) |
| **Apply** (merge or `/tofu apply`) | `TFPLS_ROLE_ARN` or `TFPLS_ENV_JSON` (an Environment can hold the apply role), optional [GitHub Environment](credentials.md) with required reviewers | apply anything but the saved, reviewed plan (when one is required): the plan file is bound to the head commit, the git tree, the workspace, the directory and the OpenTofu version, is single-use and is encrypted at rest with `PLAN_ENCRYPTION_KEY`. Only artifacts of runs on the default branch or on exactly the applied commit are accepted, so another branch's workflow cannot plant a plan under the name of your pull request |
| **A commenter** | can type `/tofu …` | apply without the approvals the workflow requires: reviews of the *current* commit, not of an older one; up-to-date branch; optionally an owner's approval (CODEOWNERS read from the default branch); only reviews of people with write access count; the commenter needs write access; how strict the review rule is, is configurable ([approvals](approvals.md)) |
| **A pull request** | can change code, config, policies | change who has to approve it (CODEOWNERS and the approval rules are read from the default branch, matched by directory and workspace, never from the pull request's own config), or the shared workflows (callers pin `@v0`, which points at a commit where internal references are exact tags) |

Other properties:

- **Fork pull requests never get credentials** and are not planned.
- **Command injection**: `/tofu` arguments are validated against a strict pattern (`-target` values especially) before they reach a shell.
- **Secrets in logs**: values of the credential maps are masked; sensitive plan values are shown as `(sensitive)` and never written to comments.
- **Runners**: self-hosted runners keep their work directory between jobs, so the tofu action deletes plan files and `.terraform` after every run; see [runners.md](runners.md) for ephemeral and containerised options. A public repository should never use self-hosted runners for pull requests.
- **The data branch** (`tfpls-data`: audit log, drift state, queue tickets) holds no secrets and no plan values, only workspace names, results, actors and run links. Anyone with write access can rewrite it; protect it with a branch rule if you rely on the audit log.
- **Supply chain**: third-party actions are pinned to commit SHAs; tools are downloaded from release URLs with SHA-256 verification.
- **Fixing a drift on demand**: anyone with write access can plan and apply a drifted workspace from its `Drift: <workspace>` issue or the manual task `fix`, without a pull request and without a second reviewer (a deliberate choice: the plan comment and the audit log are the record). The same credentials and policies apply; set `drift_fix: false` for workspaces that must go through a pull request ([drift.md](drift.md#fixing-a-drift-on-demand-no-pull-request)).
- **Not covered**: a malicious change *to the OpenTofu code* is code execution on the runner during plan (providers, `external` data sources). Plan jobs use the same single role as every other job, so treat who may open same-repo pull requests as who may run code with that role ([credentials.md](credentials.md)).

Report vulnerabilities as described in [SECURITY.md](../SECURITY.md).

## What the jobs can reach

- Every job asks only for the token permissions it calls the API with (pull request plan jobs: read the code, write comments, assume the role; no write to the data branch). The job behind a `/tofu` comment is one job for plan, apply and unlock and holds `contents: write`; a `/tofu plan` there runs the pull request's code with the default branch's OIDC subject (`ref:refs/heads/<default>`), so a trust policy that allows that subject also lets pull request code assume the role. Checkouts do not leave the token in `.git/config`.
- `tofu` and the policy tool run without the GitHub token and the plan encryption key, so a provider, an `external` data source or a module of a pull request does not find them in its environment. This is hygiene, **not isolation**: that code runs as the same user as the rest of the job and could still read the runner's process files or its work directory. The cloud credentials are in the environment by necessity. The controls that hold are the trust policy of the role, separate plan and apply roles with an Environment, and ephemeral runners ([credentials.md](credentials.md), [runners.md](runners.md)).

