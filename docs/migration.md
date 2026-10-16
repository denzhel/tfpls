# Migrating to tfpls

## From a hand-run `tofu` workflow (a root module with many `workspace select` + `apply` steps)

1. **Keep the code layout.** The default model matches "one root module, one directory per workspace": `workspaces/<name>/<name>.tf` plus the shared root files. That layout needs no config at all; for another one run `tools/onboard`, which writes the stack ([getting started](getting-started.md#another-layout)). The stack below is the long form of the same thing.
2. **Move the backend to S3 with `use_lockfile = true`** if it is not (DynamoDB locking is not supported; see [backends.md](backends.md)). Migrate state with `tofu init -migrate-state` per workspace from a trusted machine, once.
3. **Command mapping**

| Before | After |
|---|---|
| `tofu workspace select prod && tofu plan` | open a PR; read the `prod` plan comment |
| `tofu apply` | review, then `/tofu apply prod` (or merge with `auto_apply`) |
| `tofu apply -target=module.vpc` | `/tofu apply prod -target=module.vpc` |
| `tofu force-unlock <id>` | `/tofu unlock prod` |
| "did anyone change prod by hand?" | `drift.yml` |
| `workspace new` | add the directory; `create_workspace: true` for the first run only |

4. **Rollout plan**
   1. Run `tools/onboard` and open ONE pull request: it plans every workspace. Open a few more PRs and compare the comments with your own `tofu plan` output.
   2. Add `comment` (apply by comment) with `auto_apply: never` everywhere (the default). Apply a low-risk change in a dev workspace this way.
   3. Turn on `auto_apply: always` for dev, `additive_only` for staging. Production stays on `never` until the team trusts it.
   4. Look at the first scheduled drift run, the `locks` task and the dashboard (the audit log is on by default).
   5. Retire the manual process: remove direct `apply` rights of people on production, keep a break-glass role.
5. **Trial on a copy first.** Fork the repository into a scratch organisation or use a copy of the state bucket prefix, and run steps 1 to 3 there.

## Worked example: a repository with one root module and a dozen workspaces

The layout this guide was checked against has: a root module (`main.tf`, `terraform.tf`, `provider.tf`, `modules/`), one directory per tofu workspace under `workspaces/<name>/` (`<name>.tf`, `s3.tf`, `outputs.tf`, …), names like `dev-eu-west-2`, `stg-us-west-2`, `prod-us-west-1`, `dr-us-east-1`, an S3 backend with a DynamoDB lock table, a plan archive in `builds/`, a script that compares prod with its DR twin, and a written procedure for new workspaces. The conversion (for a repository without the extras below, `tools/onboard` alone is enough: `workspaces/*` is the default layout):

```yaml
# .tfpls.yml
defaults:
  auto_apply: never
stacks:
  - name: terraform
    dir: .
    workspaces_glob: "workspaces/*"                    # every directory is a workspace
    workspace_paths: ["workspaces/{workspace}/"]       # a change inside one selects only that one
    ignore: ["**/*.md", "docs/**", "scripts/**", "builds/**"]
    triggers:                                          # a prod change also plans its DR twin
      prod-us-west-1: [dr-us-east-1]
      prod-eu-central-1: [dr-eu-west-1]
    overrides:
      "dev-*":    { auto_apply: always }
      "stg-*":    { auto_apply: additive_only }
      "prod-*":   { auto_apply: never, policy_packs: [safety] }
      "dr-us-east-1": { auto_apply: never, after: ["prod-us-west-1"] }   # DR is applied only after its prod
      "dr-eu-west-1": { auto_apply: never, after: ["prod-eu-central-1"] }
```

A dry run of this file against a tree with the same directory names (empty files, no cloud) selected exactly what was expected: a change in `workspaces/dev-eu-west-2/` selected only that workspace; a change in `workspaces/prod-us-west-1/` selected it and its DR twin with the DR job waiting for prod; a change to a shared root file selected every workspace in dependency order; changes in `scripts/` and `docs/` selected nothing. Planning and applying against real accounts was not part of that check.

What else to convert:

| In the old repository | In tfpls |
|---|---|
| backend `dynamodb_table = ...` (S3 + DynamoDB lock) | only S3 native locking is supported: add `use_lockfile = true` next to `dynamodb_table` first (both locks are taken during the transition), run a few plans and applies, then delete `dynamodb_table` |
| `required_version = "1.11.4"` | write it in `.opentofu-version` (or set `tofu_version` in the caller's `with:`) so the runners use that version |
| `builds/` (saved plans in git) | not needed: reviewed plans are stored as encrypted artifacts; ignore the directory in the config |
| a script comparing prod and DR configuration with ignore lists | not part of the pipelines: keep the script and run it from your own workflow |
| the new-workspace procedure document | not part of the pipelines: a new workspace is a new directory under `workspaces/` (copy a template, commit it in a pull request); the first partial apply is `/tofu apply <workspace> -target=module.vpc` |
| applying from a laptop or a bastion | `/tofu apply` on the pull request; keep a break-glass role for emergencies |

## The old `workspaces:` list (removed in v0.6.0)

Early versions described one entry per directory under a top-level `workspaces:` key. It is gone; the run stops with one message pointing here. An entry `{ name: dev, dir: envs/dev, auto_apply: always }` becomes a stack:

```yaml
stacks:
  - name: dev
    dir: envs/dev
    workspaces: [default]      # the tofu workspace; `default` if the directory does not use workspaces
    auto_apply: always
```

For the common layout (one directory per workspace under `workspaces/`) delete the list and use `overrides:`.
