# Security model

What can reach what, and what stops it.

## What to rely on

1. **The cloud credentials are the only hard boundary.** Plans run the code of the pull request, so anyone who can push a branch to the repository runs code with the plan role. Approvals, CODEOWNERS and reviewed plans are process controls on top of that. For a real boundary give apply its own role: keep it in a GitHub Environment limited to the default branch with required reviewers and prevent-self-review, and let the role's trust policy accept only `environment:<name>` ([credentials.md](credentials.md)).
2. **Approvals.** Only approvals by users with write access count. Whether they must be of the current commit follows the default branch's ruleset: with "Dismiss stale approvals" (and when there is no pull request rule at all) only an approval of the current commit counts; without it an approval of an older commit counts too ([approvals.md](approvals.md)). The rules are read from the default branch and matched by directory and workspace, never from the pull request's own config. A pull request that points a new directory at existing state is code: only review catches that. Two accounts of one person are indistinguishable from two people. `auto_apply` and drift fixes skip approvals, so protect the default branch and set `drift_fix: false` where applies need approval.
3. **Saved plans** are bound to the commit, the tree, the directory and the OpenTofu version. Only artifacts of this repository's own runs are used. On public repositories plans are saved only encrypted; the key is readable by every workflow in the repository, so it keeps outsiders out, not writers.
4. **Public repositories.** Comments, logs and the data branch are public. Fork pull requests are never planned and get no credentials. Never use self-hosted runners.
5. **Locks.** The pipeline never force-unlocks state by itself: the `locks` task shows holders, `/tofu unlock` and the task release a named lock after the checks in [locks.md](locks.md).
6. **Supply chain.** Third-party actions are pinned by SHA, tools are verified by checksum. `@v0` moves with each release; pin an exact tag to fix it.

## Who can do what

| Actor / code | Runs with | Cannot |
|---|---|---|
| **Pull request code** (from a collaborator; never from a fork) in `plan` jobs | the role (`TFPLS_ROLE_ARN`) or the credential map (`TFPLS_ENV_JSON`); `GITHUB_TOKEN` as granted by the caller | run at all from a fork (plans for fork PRs are skipped by the fork guard); anything the role's trust policy and permissions do not allow. **It can use the role**: see [credentials.md](credentials.md) |
| **Apply** (merge or `/tofu apply`) | `TFPLS_ROLE_ARN` or `TFPLS_ENV_JSON` (an Environment can hold the apply role), optional [GitHub Environment](credentials.md) with required reviewers | apply anything but the saved, reviewed plan (when one is required): the plan file is bound to the head commit, the git tree, the workspace, the directory and the OpenTofu version, is single-use and is encrypted at rest with `PLAN_ENCRYPTION_KEY`. Only artifacts of runs on the default branch or on exactly the applied commit are accepted, so another branch's workflow cannot plant a plan under the name of your pull request |
| **A commenter** | can type `/tofu …` | apply without the approvals the workflow requires: reviews of the *current* commit when the ruleset dismisses stale approvals (or has no pull request rule), otherwise reviews of any commit; up-to-date branch; optionally an owner's approval (CODEOWNERS read from the default branch); only reviews of people with write access count; the commenter needs write access; how strict the review rule is, is configurable ([approvals](approvals.md)) |
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

## Automated scanning

GitHub's free security scanners run on the tfpls repository itself. They look for known kinds of mistakes and report them as alerts on the repository's Security tab. None of them changes or merges code by itself (the only thing that blocks is push protection, which refuses a push that contains a token), and none of them replaces review or the boundaries above. Here they cost nothing, because the repository is public.

### What runs and when

| Scanner | What it checks | When it runs | AI |
|---|---|---|---|
| **CodeQL** (code scanning, default setup) | the workflows and composite actions (CodeQL's language `actions`): text from outside reaching a shell, pull request code run in a privileged job, poisoned caches and artifacts, secrets handed to a job that does not need them, known vulnerable actions | every push to `main`; every pull request into `main` from a branch of this repository (not from a fork), again on each new commit; once a week | no |
| **Copilot Autofix** | a CodeQL alert | when CodeQL reports an alert on a pull request; for an alert on `main`, when someone asks for a fix | **yes** |
| **Secret scanning** and **Push protection** | the whole git history on all branches, and the text of pull requests, for tokens in the known formats of service providers | every push (push protection rejects a push that contains such a token); the history again when GitHub adds a new format | no |
| **Dependabot alerts** | the dependencies GitHub finds in the repository against the reviewed advisories of the GitHub Advisory Database | when an advisory is added, and when the dependencies change | no |
| **Dependabot security updates** | an alert for which a fixed version exists | after such an alert: opens a pull request that moves the dependency to the fixed version | no |
| **Private vulnerability reporting** | a report sent through Security → Report a vulnerability ([SECURITY.md](../SECURITY.md)) | when someone reports | no |

For an action, GitHub raises a Dependabot alert only when it is referenced by a version tag, not by a commit SHA, and tfpls pins every third-party action by SHA. So in this repository Dependabot alerts and security updates cover what may be added later; the pinned actions are kept current by Dependabot *version updates* ([dependencies.md](dependencies.md#dependabot)).

Not on for tfpls: *generic patterns* (private keys, connection strings) and *validity checks* (GitHub asks the provider whether a found token still works), which GitHub does not turn on for this repository, a public repository of a personal account; *AI-detected passwords*, offered to organization repositories with GitHub Secret Protection; *AI Scan for pull requests*, which is billed per use ([below](#how-it-works-and-what-is-ai)).

### Why on every pull request and every week

**Every pull request**, because a mistake is cheapest to fix before it is merged. New alerts show up as annotations on the changed lines while the author still has the change in mind, and Copilot Autofix can suggest the fix right there. The scan after the merge keeps the list of open alerts equal to what is on `main`: an alert closes by itself when the commit that removes the problem lands.

**Every week**, because the code may not have changed, but what the scanners know has. CodeQL gets new and better queries, GitHub adds advisories and token formats. The weekly scan checks unchanged code with the current queries, so a mistake nobody knew how to find when the code was written is still found. Dependabot and secret scanning do the same by themselves when an advisory or a format is added. GitHub stops the weekly CodeQL scan when no push or pull request started a scan for 180 days.

### How it works and what is AI

- **CodeQL: no AI.** CodeQL first turns the code into a database (for workflows: which event starts a job, which values flow into which script or action input, which permissions and secrets a job gets), then runs queries over it. Each query describes one known mistake; a match becomes an alert with the file, the line, an explanation and a recommended fix. The same code and the same queries always give the same alerts. Default setup runs the `default` query suite: precise queries that rarely report a false positive. CodeQL does not read OpenTofu code; HCL is not one of its languages.
- **Copilot Autofix: AI, and only a suggestion.** For an alert, Autofix gives a large language model the alert, the code around it and the help text of the query, and shows the proposed change on the pull request or the alert. A model can be wrong: the change may not remove the problem, or may add a new one. So a person reviews, tests and accepts it like any other change; nothing is committed without that person. It needs no Copilot subscription, it is on wherever CodeQL runs, and GitHub states that the data is not used to train models.
- **Secret scanning, push protection and Dependabot: no AI.** They match known patterns: the token formats of service providers, and the versions named in reviewed advisories.
- **AI features that are off.** *AI-detected passwords* (Copilot secret scanning) uses AI to find passwords, which have no fixed format; GitHub offers it to organization repositories with GitHub Secret Protection. *AI Scan for pull requests* has an AI model read pull requests in languages CodeQL does not cover, such as shell scripts and Terraform (HCL); its findings are advisory and may be false positives. It is available for public repositories of personal accounts, but it is billed per scanned pull request in AI credits, so it stays off unless the owner decides otherwise.

### What it costs

- **tfpls** (public): nothing. CodeQL runs as a GitHub Actions run on a standard GitHub-hosted runner, and GitHub Actions is free for public repositories that use standard GitHub-hosted runners; the other scanners are free for public repositories too. AI Scan, the one metered feature, is off.
- **A private repository** (for example the repository with your infrastructure code): Dependabot alerts, security updates and version updates are free on every plan. Code scanning with Copilot Autofix needs GitHub Code Security, and secret scanning with push protection needs GitHub Secret Protection: paid products that GitHub sells to organizations on GitHub Team and GitHub Enterprise Cloud. Push protection for users (a personal setting, on by default) covers pushes to public repositories only. AI Scan is not available for a private repository of a personal account.
- **A public repository that calls tfpls** gets the same scanners free (Settings → Advanced Security). There CodeQL checks the workflow files; the OpenTofu code is what the [policies](policies.md) check, in the plan.

### What a person still does

1. **Decides every alert.** Fix the problem, or dismiss the alert with a reason (false positive, used in tests, won't fix) and a comment that says why.
2. **Revokes a leaked secret.** Closing the alert or rewriting history does not make the token safe: revoke or rotate it at the provider first.
3. **Reviews Dependabot pull requests** like any other: CI runs on them, a person merges.
4. **Reviews an Autofix suggestion** like code from a new contributor: read it, run the tests, change it where needed.
5. **Answers private vulnerability reports** as [SECURITY.md](../SECURITY.md) promises.
6. **Reads the check `CodeQL`** of a pull request (GitHub's code scanning results; the job that produces them is `Analyze (actions)`). It fails when the pull request adds an alert of severity error, critical or high, but it is not one of the required checks (`lint`, `tests`), so it does not block a merge on its own.

Sources: [GitHub security features](https://docs.github.com/en/code-security/getting-started/github-security-features), [setup types for code scanning](https://docs.github.com/en/code-security/concepts/code-scanning/setup-types) (when default setup scans), [configuring default setup](https://docs.github.com/en/code-security/code-scanning/enabling-code-scanning/configuring-default-setup-for-code-scanning), [about code scanning with CodeQL](https://docs.github.com/en/code-security/code-scanning/introduction-to-code-scanning/about-code-scanning-with-codeql) (languages), [CodeQL query suites](https://docs.github.com/en/code-security/code-scanning/managing-your-code-scanning-configuration/codeql-query-suites), [Copilot Autofix](https://docs.github.com/en/code-security/code-scanning/managing-code-scanning-alerts/about-autofix-for-codeql-code-scanning), [code scanning on pull requests](https://docs.github.com/en/code-security/code-scanning/managing-code-scanning-alerts/triaging-code-scanning-alerts-in-pull-requests), [secret scanning](https://docs.github.com/en/code-security/secret-scanning/introduction/about-secret-scanning), [AI-detected passwords](https://docs.github.com/en/code-security/how-tos/secure-your-secrets/detect-secret-leaks/enabling-ai-powered-generic-secret-detection), [AI Scan for pull requests](https://docs.github.com/en/code-security/concepts/code-scanning/ai-powered-security-detections), [Dependabot alerts](https://docs.github.com/en/code-security/concepts/supply-chain-security/dependabot-alerts) (actions pinned by SHA), [billing for GitHub Actions](https://docs.github.com/en/billing/concepts/product-billing/github-actions).

