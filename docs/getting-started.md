# Getting started

For a repository that already works with `tofu` the files take a minute; the first AWS role and variable are the slow part (allow an hour if you have not set up GitHub OIDC before).

Two things to know before you start:

- **Who can run what.** A pull request plan runs the code of the pull request with the pipeline's IAM role, so anyone who can open a pull request in the repository can use that role: restrict its trust policy to this repository ([credentials.md](credentials.md#the-trust-policy)). To keep plan code away from apply power, use a read-only plan role ([credentials.md](credentials.md#optional-approvals-that-cannot-be-bypassed-a-read-only-plan-role)).
- **Working alone.** `/tofu apply` needs an approving review of the latest commit by someone other than the author, and GitHub does not let you approve your own pull request, so your first apply is refused. The number of approvals is the one your repository asks for (the pull request rule of a ruleset on the default branch; 1 when there is none). Add a second account as a collaborator, set the ruleset's required approvals to 0, or, as an admin, comment `/tofu apply <workspace> --now` ([approvals.md](approvals.md)).

## 1. Run `onboard`

In the repository (root module, one directory per tofu workspace under `workspaces/`):

```sh
curl -fsSL https://raw.githubusercontent.com/denzhel/tfpls/v0/tools/onboard | bash
# or from a checkout: tools/onboard [--ref v0] [dir]
```

It writes the caller (and a config when needed) and changes nothing else (no network, no secrets):

- `.github/workflows/tofu.yml`, the whole caller (about 20 lines: the triggers and permissions GitHub requires in the caller, and one `uses: denzhel/tfpls/.github/workflows/tofu.yml@v0` with `secrets: inherit`).

  Pull requests plan, `/tofu` comments act, a push to main applies, the schedule runs drift detection and refreshes the dashboard, and the manual run offers `drift`, `locks` (shows who holds each state lock; releases the ones you name), `dashboard` and `fix` (fixes a drifted workspace, see [drift.md](drift.md#fixing-a-drift-on-demand-no-pull-request)). Overrides go in `with:` (`runs_on`, `tofu_version`, `apply_environment`, `queue`, `max_parallel`, `timeout_minutes`, ...: the table in the [reference](reference.md#workflow-inputs)); a GitHub limitation keeps triggers and permissions in the caller. Jobs run on `ubuntu-latest` unless you set `runs_on` (for example `self-hosted`, see [runners.md](runners.md)).
- `.tfpls.yml`, only when your layout needs one and none exists: `workspaces/<name>/` folders need no file (new folders are picked up on their own); one root module with a var file per workspace gets a `var_file` stack; directories with a backend block (`envs/<name>`) become stacks on the `default` workspace, with `watch` computed from their relative module sources.

It looks around first (layout, S3 backend and locking, OpenTofu version), prints what it did in colour on a terminal (`NO_COLOR=1` switches that off), and ends with the steps that are left, in order: the role (with the trust policy for your repository, both subject forms), the plan key, ONE pull request (the pull request that introduces the pipeline plans every workspace), approvals, and an optional first drift run. `--runs-on <label>` changes the runner (default `ubuntu-latest`; `ubuntu-slim` is cheaper but GitHub cuts every job on it after 15 minutes, so use it only when every plan and apply is quick).

Conventions that need no configuration:

| Without config | Default |
|---|---|
| Layout | root module, one workspace per `workspaces/*` directory, a change inside it selects only that workspace |
| Apply | never automatic: a person comments `/tofu apply` on an approved pull request |
| OpenTofu version | `.opentofu-version`, `.tofu-version`, `.terraform-version` or an exact `required_version`, else 1.10.6 |
| New workspaces | a tofu workspace that does not exist yet fails with "set create_workspace: true to create it" (so a typo cannot create empty state): put `defaults: { create_workspace: true }` in `.tfpls.yml` for the first run and remove it afterwards |
| Cloud credentials | one IAM role from the variable `TFPLS_ROLE_ARN` (OIDC), or the secret `TFPLS_ENV_JSON`; every job uses the same one |

## 2. Write only the exceptions

`.tfpls.yml` at the repository root (full reference in the [reference](reference.md#config-reference)):

```yaml
overrides:
  "dev-*":  { auto_apply: always }
  "prod-*": { policy_packs: [safety, aws-tags], drift_fix: false }
```

See [Another layout](#another-layout) if your repository is not `workspaces/<name>/`.

## Another layout

The default is one folder per tofu workspace under `workspaces/` (the folder name is the tofu workspace name; the root module is the repository root). `tools/onboard` writes the right `stacks:` for the other common layouts; this is what they look like:

```yaml
# separate root modules, one directory each (envs/dev, envs/prod), the tofu workspace is `default`
stacks:
  - { name: dev,  dir: envs/dev,  workspaces: [default], watch: [modules] }
  - { name: prod, dir: envs/prod, workspaces: [default], watch: [modules] }
```

```yaml
# one root module, one var file per workspace (vars/dev.tfvars, vars/prod.tfvars)
stacks:
  - name: app
    dir: .
    workspaces: [dev, prod]
    var_file: "vars/{workspace}.tfvars"
    workspace_paths: ["vars/{workspace}.tfvars"]   # a change to vars/dev.tfvars plans only dev; any other file plans all
```

`watch` lists extra directories (modules the stack uses) whose changes plan the stack. Labels: a stack whose only workspace is `default` is called by its stack name (`/tofu apply dev`); other workspaces by their name. Settings in `overrides:` match a workspace name or a stack name.

## 3. Credentials

One IAM role that can plan and apply (see [credentials.md](credentials.md) for the trust policy and what it means for pull requests), as the variable `TFPLS_ROLE_ARN`, or one secret `TFPLS_ENV_JSON` with a JSON map of environment variables. Optional secrets:

| Secret | What |
|---|---|
| `PLAN_ENCRYPTION_KEY` | optional for private repositories, **required for public ones** (no plan is saved without it): `openssl rand -base64 32 \| gh secret set PLAN_ENCRYPTION_KEY`; saved plans are encrypted with it |

Details and the reasoning: [credentials.md](credentials.md), [security-model.md](security-model.md). The state backend must be S3 with native locking (`use_lockfile = true`): [backends.md](backends.md).

## 4. Try it

1. Open a pull request that changes a file inside one `workspaces/<name>/` directory: one plan comment per changed workspace appears, showing only what changes.
2. Comment `/tofu apply <workspace>`. By default it needs an approving review of the latest commit from **someone other than the author** (GitHub does not let you approve your own pull request): in a repository with one person, add a second account as a collaborator, set the ruleset's required approvals to 0, or, as an admin, add `--now` ([approvals](approvals.md)).
3. Check the run: `plan / <workspace>` rows only for the workspaces that changed.

If something does not look right: [runbooks.md](runbooks.md).

Checked from scratch: this guide was followed in a new private repository with a local S3-compatible backend (native locking) and one self-hosted runner: the plan comment showed only the changed attribute, `/tofu apply dev` applied it after a second account approved, and the merge ran cleanly.
