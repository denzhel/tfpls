# Getting started

For a repository that already works with `tofu` the files take a minute; the first AWS role and variable are the slow part (allow an hour if you have not set up GitHub OIDC before).

Two things to know before you start:

- **Who can run what.** A pull request plan runs the code of the pull request with the pipeline's IAM role, so anyone who can open a pull request in the repository can use that role: restrict its trust policy to this repository ([credentials.md](credentials.md#the-trust-policy)). To keep plan code away from apply power, use a read-only plan role ([credentials.md](credentials.md#optional-approvals-that-cannot-be-bypassed-a-read-only-plan-role)).
- **Working alone.** `/tofu apply` needs an approving review of the latest commit by someone other than the author, and GitHub does not let you approve your own pull request, so your first apply is refused. The number of approvals is the one your repository asks for (the pull request rule of a ruleset on the default branch; 1 when there is none). Add a second account as a collaborator, set the ruleset's required approvals to 0, or, as an admin, comment `/tofu apply <workspace> --now` ([approvals.md](approvals.md)).

## 1. Run `onboard`

In the root of the repository:

```sh
curl -fsSL https://raw.githubusercontent.com/denzhel/tfpls/v0/tools/onboard | bash
# with options under curl: ... | bash -s -- --dry-run
# from a checkout, without any download: go run ./cmd/tfpls onboard [--yes] [--dry-run] [dir]
```

`tools/onboard` is a short script (read it before you pipe it into a shell): it finds your OS and architecture (Linux or macOS, amd64 or arm64), downloads the `tfpls` binary of a release with `SHA256SUMS`, **refuses to run it when the checksum does not match**, keeps it under `~/.cache/tfpls/<version>/` and runs `tfpls onboard` with your options. If `gh` is installed it also says whether `gh attestation verify` confirms the build provenance (informational, never required). The default is the latest release; `--ref vX.Y.Z` (or `TFPLS_REF=vX.Y.Z`) takes that release, and a tag that has no binary falls back to the latest. A version already in the cache needs no network. Offline or from a source build, point it at a binary: `TFPLS_ONBOARD_BIN=./tfpls tools/onboard --dry-run`. The questions are read from the terminal (`/dev/tty`), so they work under `curl | bash`.

It looks around first and says what it understood: the layout, the state backend and its locking, the providers, the OpenTofu version the pipeline will use, local modules, existing workflows, CODEOWNERS and the default branch. Then it shows the files it would write and asks before it writes them (Enter takes the default; under `curl | bash` the answers are read from the terminal). It writes:

- `.github/workflows/tofu.yml`, the whole caller (about 20 lines: the triggers and permissions GitHub requires in the caller, and one `uses: denzhel/tfpls/.github/workflows/tofu.yml@v0` with `secrets: inherit`).

  Pull requests plan, `/tofu` comments act, a push to main applies, the schedule runs drift detection and refreshes the dashboard, and the manual run offers `drift`, `locks` (shows who holds each state lock; releases the ones you name), `dashboard` and `fix` (fixes a drifted workspace, see [drift.md](drift.md#fixing-a-drift-on-demand-no-pull-request)). Overrides go in `with:` (`runs_on`, `tofu_version`, `apply_environment`, `queue`, `max_parallel`, `timeout_minutes`, ...: the table in the [reference](reference.md#workflow-inputs)); a GitHub limitation keeps triggers and permissions in the caller. Jobs run on `ubuntu-latest` unless you set `runs_on` (for example `self-hosted`, see [runners.md](runners.md)).
- `.tfpls.yml`, only when your layout needs one and none exists: `workspaces/<name>/` folders need no file (new folders are picked up on their own); one root module with a var file per workspace gets a `var_file` stack; directories with a backend block (`envs/<name>`) become stacks on the `default` workspace, with `watch` computed from their relative module sources; one root module with a single state becomes one stack named after the repository (see [Another layout](#another-layout)).

**Safe to run again.** A second run changes nothing and says so. A workflow that differs from the recommended caller is shown as a diff and replaced only when you say yes (or pass `--force`); a tfpls caller under another file name is updated in place, never doubled; `.tfpls.yml` is never overwritten; another workflow is never deleted (if it applies too, you are asked whether you retire it, and `git rm` goes on your checklist). The questions and their defaults: write the new files (yes), the name of a one-root-module stack (the repository name), replace a differing workflow (no), retire another pipeline that applies (no), the GitHub step (no), set the plan key (yes).

It ends with a checklist of what is done and what is left, in order: the credentials (for AWS the role with the trust policy for your repository, both subject forms; without AWS the `TFPLS_ENV_JSON` secret, see [below](#one-root-module-no-aws-cloudflare-state-in-r2)), the plan key, ONE pull request (the pull request that introduces the pipeline plans every workspace), approvals, and an optional first drift run.

| Option | What |
|---|---|
| `--yes` | no questions: every default is taken (a differing workflow is kept, nothing goes online) |
| `--dry-run` | shows what would change, as diffs, and writes nothing |
| `--force` | replaces a differing caller workflow without asking |
| `--no-jokes` | no joke at the end |
| `--runs-on <label>` | the runner (default `ubuntu-latest`; `ubuntu-slim` is cheaper but GitHub cuts every job on it after 15 minutes, so use it only when every plan and apply is quick); a JSON array of labels works too |
| `--ref`, `--slug` | the tfpls version and repository the caller uses (default `v0`, `denzhel/tfpls`) |

Without a terminal (CI, a pipe) it asks nothing and takes the defaults. Colours only on a terminal (`NO_COLOR=1` switches them off).

It works offline. Only if you say yes to its optional GitHub step, and `gh` is logged in, it reads the numeric owner and repository IDs (for the exact trust policy subject), checks whether the variable `TFPLS_ROLE_ARN` and the secrets `PLAN_ENCRYPTION_KEY` and `TFPLS_ENV_JSON` exist, and offers to set the plan key (generated on your machine and piped to `gh secret set`, never shown; an existing key is never replaced) and the role ARN.

Conventions that need no configuration:

| Without config | Default |
|---|---|
| Layout | root module, one workspace per `workspaces/*` directory, a change inside it selects only that workspace |
| Apply | never automatic: a person comments `/tofu apply` on an approved pull request |
| OpenTofu version | `.opentofu-version`, `.tofu-version`, `.terraform-version` or an exact `required_version`, else 1.13.2 |
| New workspaces | a tofu workspace that does not exist yet fails with "workspace ... does not exist: create it once with `tofu workspace new <name>`": tfpls never creates a workspace, so a typo cannot create empty state |
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

```yaml
# one root module in the repository root with a single state (no workspaces): one stack on the `default` workspace,
# named after the repository
stacks:
  - name: edge-infra
    dir: .
    workspaces: [default]
    ignore: ["**/*.md", "docs/**", ".github/**", "LICENSE*", ".gitignore"]   # changes here plan nothing
```

`watch` lists extra directories (modules the stack uses) whose changes plan the stack. Labels: a stack whose only workspace is `default` is called by its stack name (`/tofu apply dev`); other workspaces by their name. Settings in `overrides:` match a workspace name or a stack name.

## One root module, no AWS (Cloudflare, state in R2)

A repository with one root module, one state and a provider other than AWS, for example Cloudflare with the state in Cloudflare R2 through `backend "s3"` with `endpoints` and `use_lockfile = true`, is onboarded as the one root stack above, and nothing needs an AWS role. Put every key into one secret, `TFPLS_ENV_JSON`, a JSON map of environment variables:

```sh
gh secret set TFPLS_ENV_JSON < env.json   # then delete env.json; never commit it
```

```json
{"CLOUDFLARE_API_TOKEN": "<API token>", "AWS_ACCESS_KEY_ID": "<R2 access key ID>", "AWS_SECRET_ACCESS_KEY": "<R2 secret access key>"}
```

The `AWS_*` pair is the R2 API token of the state bucket (the S3 backend reads these names); it must be able to write the lock object (`<key>.tflock`) next to the state. Pin OpenTofu with an exact `required_version` or a `.opentofu-version` file (onboarding shows which version the pipeline will use), and apply with `/tofu apply <stack>`, by default the repository name. Onboarding never prints the endpoint, which carries your account ID. R2 itself is not verified yet: see [backends.md](backends.md).

## 3. Credentials

One IAM role that can plan and apply (see [credentials.md](credentials.md) for the trust policy and what it means for pull requests), as the variable `TFPLS_ROLE_ARN`, or one secret `TFPLS_ENV_JSON` with a JSON map of environment variables (the only one needed [without AWS](#one-root-module-no-aws-cloudflare-state-in-r2)). Optional secrets:

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
