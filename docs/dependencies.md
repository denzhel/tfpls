# Dependencies

How tfpls keeps the actions it pins and the tools it installs up to date, and how to do the same in a repository that calls tfpls. Nothing merges by itself: every update is a pull request that CI must pass and a person reviews. Dependabot alerts, security updates and the other scanners are in the [security model](security-model.md#automated-scanning).

## Dependabot

[`.github/dependabot.yml`](../.github/dependabot.yml) asks Dependabot for version updates of every action tfpls pins. Each setting and the official page behind it:

| Setting | Value | Why |
|---|---|---|
| `package-ecosystem` | `github-actions` | the only manifests here are the workflows and the composite actions |
| `directories` | `/` and `/actions/*` | `/` reads `.github/workflows` and a root `action.yml`; any other listed directory has its own YAML files read, so the composite actions (`actions/<name>/action.yml`) are covered only when listed. Globs need `directories`, not `directory` ([options reference](https://docs.github.com/en/code-security/dependabot/working-with-dependabot/dependabot-options-reference), `directories`) |
| `schedule` | weekly, Monday 05:00 UTC | a fixed quiet time instead of the random one Dependabot assigns by default (`schedule`) |
| `groups` | minor and patch updates of all actions in one pull request; each major update in its own, one per action across the directories (`group-by: dependency-name`) | routine bumps cost one review; a major (a possible breaking change, often a new Node.js runtime) is looked at alone. Without `group-by` an action used in a workflow and in a composite action would get one pull request per directory ([optimizing pull requests](https://docs.github.com/en/code-security/tutorials/secure-your-dependencies/optimizing-pr-creation-version-updates), [group-by](https://github.blog/changelog/2026-02-24-dependabot-can-group-updates-by-dependency-name-across-multiple-directories/)) |
| `cooldown` | `default-days: 5` | a release is proposed only once it is 5 days old, which leaves time for a broken or hijacked release to be found and pulled. GitHub's default is 3; `github-actions` supports `default-days` only, not the semver-specific days (`cooldown`) |
| `open-pull-requests-limit` | 5 | GitHub's default, written down (`open-pull-requests-limit`) |
| `labels` | `dependencies` | once `labels` is set, a label that does not exist in the repository is ignored, so it was created (`gh label create dependencies`) (`labels`) |
| `commit-message` | `prefix: deps` | commits and titles read `deps: bump ...` (`commit-message`) |
| auto-merge | none | tfpls is CI code that runs with its callers' cloud credentials: an update reaches `main` only after CI passed and a maintainer read the release notes |

**SHA pins and their comments.** Every third-party action is pinned to a full commit SHA with its version as a comment on the same line (`tests/pinning.test.sh`, `tests/dependabot.test.sh`). Dependabot keeps that form: it moves the pin to the commit of the newest release tag and "updates the version documentation of GitHub Actions when the comment is on the same line" ([supported ecosystems](https://docs.github.com/en/code-security/dependabot/ecosystems-supported-by-dependabot/supported-ecosystems-and-repositories#github-actions)). Pin release commits only: a commit "not associated with any tag" is moved to the newest commit of the default branch instead.

**What it does not cover.**

- Dependabot "will not create alerts for actions pinned to SHA values" ([secure use reference](https://docs.github.com/en/actions/reference/security/secure-use)), so security updates do not reach our pins either: a fixed action arrives as a version update, after the cooldown. For an urgent fix, bump the pin by hand.
- tfpls's own references (`denzhel/tfpls/actions/<name>@main`) name a branch, not a version, and are left alone; a release pins them ([releasing](releasing.md)). Local `./` references are ignored.
- The tools tfpls downloads (OpenTofu, conftest) and actionlint in CI are in no manifest Dependabot reads: see [tool versions](#tool-versions).

**Sign-off.** Dependabot signs its commits off itself (its commit messages end with `Signed-off-by: dependabot[bot] <support@github.com>`), so its pull requests already meet the [DCO sign-off](../CONTRIBUTING.md#licence-and-sign-off) asked of contributors and need no amending. Do not push commits onto a Dependabot branch: "Dependabot will stop rebasing a pull request once extra commits have been pushed to it" ([managing pull requests for dependency updates](https://docs.github.com/en/code-security/dependabot/working-with-dependabot/managing-pull-requests-for-dependency-updates)).

**Checking it.** Dependabot checks for updates once the file is checked in or changed on the default branch ([actions guide](https://docs.github.com/en/code-security/dependabot/working-with-dependabot/keeping-your-actions-up-to-date-with-dependabot)). Insights, Dependency graph, Dependabot shows the monitored files, when Dependabot last checked, and the job logs; a package manager missing there means the configuration is wrong ([checking the status](https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/secure-your-dependencies/configure-version-updates#checking-the-status-of-version-updates)). There is no API to start a version-update run, so the schedule itself is proven by the first scheduled run.

## Tool versions

The tools tfpls installs are not in any manifest, so a weekly workflow watches them. [`.github/workflows/tool-versions.yml`](../.github/workflows/tool-versions.yml) runs [`tools/tool-versions.sh`](../tools/tool-versions.sh) every Monday at 05:23 UTC, and by hand:

```bash
gh workflow run tool-versions.yml --repo denzhel/tfpls
```

| Tool | Default | Compared with |
|---|---|---|
| OpenTofu | `TOFU_VERSION` in `actions/setup/install.sh` | releases of `opentofu/opentofu` |
| conftest | `CONFTEST_VERSION` in `actions/setup/install.sh` | releases of `open-policy-agent/conftest` |
| actionlint | `v=` in `.github/workflows/ci.yml` | releases of `rhysd/actionlint` |

It takes the highest published version among the newest 100 releases: drafts, pre-releases and tags that are not `X.Y.Z` are skipped, and a backport of an older line released later does not count as newer. Then it keeps exactly one open issue titled **Tool versions behind** (label `dependencies`): opened when a default is behind, edited only when the list changes, closed with a comment when every default is current, and left alone (nothing done at all) when everything is current and no issue is open. When a release list or a default cannot be read, the run fails instead of reporting "current".

It holds `contents: read` and `issues: write` only and never edits a file: raising a default changes what every caller downloads, so it is a reviewed pull request (the issue says which lines to change; for actionlint also its checksum). Tests: `tests/tool-versions.test.sh`, against a fake `gh`.

## In your repository

A ready-to-copy `.github/dependabot.yml` for a repository that calls tfpls. Keep the blocks you need: Dependabot reports an error for a block whose files it cannot find.

```yaml
version: 2
updates:
  # the workflows, including the tfpls version you call (denzhel/tfpls/.github/workflows/tofu.yml@v0)
  - package-ecosystem: github-actions
    directory: "/"
    schedule: { interval: weekly, day: monday, time: "05:00", timezone: Etc/UTC }
    cooldown: { default-days: 5 }
    groups:
      actions-minor-patch: { patterns: ["*"], update-types: [minor, patch] }
    labels: [dependencies]
    commit-message: { prefix: deps }

  # OpenTofu providers and modules in .tf and .tofu files; .terraform.lock.hcl is updated with them
  - package-ecosystem: opentofu
    directories: ["/terraform/*"]   # where your workspaces are; globs allowed, for example "/envs/*" or "/modules/*"
    schedule: { interval: weekly, day: monday, time: "05:00", timezone: Etc/UTC }
    cooldown: { default-days: 5 }
    groups:
      providers-minor-patch: { patterns: ["*"], update-types: [minor, patch] }
      providers-major: { patterns: ["*"], update-types: [major], group-by: dependency-name }
    labels: [dependencies]
    commit-message: { prefix: deps }

  # pre-commit hooks, if the repository has a .pre-commit-config.yaml
  - package-ecosystem: pre-commit
    directory: "/"
    schedule: { interval: weekly, day: monday, time: "05:00", timezone: Etc/UTC }
    cooldown: { default-days: 5 }
    labels: [dependencies]
    commit-message: { prefix: deps }
```

Create the label once (`gh label create dependencies`): with `labels` set, a missing label is ignored. Notes from the [options reference](https://docs.github.com/en/code-security/dependabot/working-with-dependabot/dependabot-options-reference) and [supported ecosystems](https://docs.github.com/en/code-security/dependabot/ecosystems-supported-by-dependabot/supported-ecosystems-and-repositories#opentofu):

- `github-actions` also covers the tfpls line of your workflow. On `@v0` you get every tfpls `0.x` release through the moving tag, and a pull request to `@v1` when a new major version appears: read the [changelog](../CHANGELOG.md) before merging it. On an exact tag or a SHA, each release comes as a pull request.
- `opentofu` is maintained by the OpenTofu community and has version updates only (no security updates, no private registries); `terraform` is the ecosystem for code that uses the Terraform registry.
- A provider update changes infrastructure code: review its plan like any other change, which for a Dependabot pull request needs one extra step (next section).

## Dependabot pull requests and the plan

GitHub runs the workflows of a Dependabot pull request (`pull_request`, `push` and review events) "as though they are from a forked repository" ([events that trigger workflows](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#pull_request)): the `GITHUB_TOKEN` is read-only unless the workflow's `permissions` raise it, which the caller written by `tools/onboard` does ([changelog](https://github.blog/changelog/2021-10-06-github-actions-workflows-triggered-by-dependabot-prs-will-respect-permissions-key-in-workflows/)), and "the only secrets available to the workflow are Dependabot secrets. GitHub Actions secrets are not available" ([troubleshooting Dependabot on GitHub Actions](https://docs.github.com/en/code-security/dependabot/troubleshooting-dependabot/troubleshooting-dependabot-on-github-actions)). For the plan of the pull request that means:

- `TFPLS_ENV_JSON`, `PLAN_ENCRYPTION_KEY` and the Slack webhook are empty. With keys in `TFPLS_ENV_JSON`, the plan has no cloud credentials and fails.
- With OIDC (`TFPLS_ROLE_ARN`), GitHub's documentation does not say whether such a run gets an OIDC token: the [workflow syntax reference](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#permissions-for-workflow-runs-triggered-by-dependabot) treats it like a fork run, where a write permission such as `id-token: write` normally cannot be granted. Do not count on it. Without a token the plan stops with `OIDC needs the job permission id-token: write`, which is misleading here (the caller does grant it). With one, the plan still runs without `PLAN_ENCRYPTION_KEY`: a public repository does not save it, and on a private one a plan saved without the key is refused for apply while the key is set ([reviewed plans](reviewed-plans.md#why-encrypt)).
- Re-running the job does not help: "When you manually re-run a Dependabot workflow, it will run with the same privileges as before."

**The way out: a maintainer comments `/tofu plan`** (or `/tofu plan <workspace>`) on the Dependabot pull request, after reading its diff and the release notes (the plan runs the new provider or module with your role). The comment is an `issue_comment` event of the maintainer, not of Dependabot, so the job gets the caller's permissions and secrets: it checks that the commenter has write access and that the pull request comes from the same repository (a Dependabot branch does; forks are refused), plans with the default branch's OIDC subject, which the trust policy of [credentials.md](credentials.md#the-trust-policy) allows, and saves the reviewed plan. From there it is a pull request like any other: approve, then `/tofu apply <workspace>` or merge. If Dependabot rebases the pull request afterwards (a new commit), comment `/tofu plan` again: a saved plan belongs to one commit.

Storing the secrets a second time as Dependabot secrets would let the pull request plan by itself; tfpls does not recommend it, because a dependency update would then run with your cloud role before anyone has looked at it.
