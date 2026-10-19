# Reviewed plans

`/tofu apply` and apply-on-merge apply **the plan that was planned, policy-checked and shown on the pull request**, not a fresh plan made at apply time. If the code or the state moved since the plan was made, tfpls refuses (or, after a merge, re-plans, see below) instead of applying something nobody reviewed.

## How it works

1. **Plan.** `plan.yml` (on every PR push) and `/tofu plan` save the plan file as a workflow artifact named `tfpls-plan-<workspace>-<pr head sha>-<targets>`, together with a small `meta.json` (workspace, directory, tofu version, git tree, targets, time).
2. **Apply.** `/tofu apply <workspace>` downloads that artifact, checks it, and runs `tofu apply` on the saved file:
   - the artifact exists and has not expired (retention 14 days);
   - it was made for the same code (the git tree of the plan equals the tree being applied), workspace, directory and tofu version;
   - it decrypts and passes its integrity check (see below);
   - policies are evaluated again against the saved plan;
   - OpenTofu itself rejects the plan if the state changed after it was made ("saved plan is stale"). A plan can therefore be applied only once.
3. **After merge.** `apply.yml` finds the merged PR, looks up its plan, and applies it when it still matches the merged code. If there is no usable plan (direct push, branch was behind, expired):
   - default: it **re-plans and applies under the workspace's `auto_apply` rule**, and says so in the job summary and logs;
   - with `require_reviewed_plan: true` on the stack or an override (for example `prod-*`) it **refuses**.

```yaml
stacks:
  - name: terraform
    overrides:
      "prod-*": { auto_apply: additive_only, require_reviewed_plan: true }
```

`/tofu apply` itself never re-plans. For a `-target` apply, plan with the same targets first: `/tofu plan prod-us-west-1 -target=module.vpc`, then `/tofu apply prod-us-west-1 -target=module.vpc`.

The caller workflow needs `actions: read` (to find and download the artifact). Nothing else changes in everyday use: plan comments and policy checks behave as before, plus one line saying the reviewed plan was saved.

## What a plan contains

A plan is the difference between the **state** of the workspace and the **merged result** of the pull request (its code on top of the default branch). It is not the pull request's diff. So a plan can also contain changes that are already on the default branch but were never applied: with `auto_apply: never` a merge applies nothing, and with `additive_only` a merged update or delete is left to a person. The next pull request that touches the workspace plans them too, and `/tofu apply` applies the whole plan, including them.

Example from a test repository: a pull request added one prefix list to a `never` workspace. Its plan said `2 to add`, because a launch template merged earlier had never been applied, and `/tofu apply` created both.

How to recognise it: the counts in the `Plan:` line are larger than what the pull request changes, or the plan lists resources the diff does not mention. What to do: read the whole plan, it is what will be applied. If the extra changes are not wanted, revert them on the default branch first. If they are, apply knowingly, or apply them first through a pull request of their own that touches the workspace. See [the runbook](runbooks.md#the-plan-shows-resources-my-pull-request-did-not-change).

## Why encrypt

A saved plan is a binary file that can contain **secret values in plaintext** (database passwords, generated keys, anything marked sensitive). Workflow artifacts can be downloaded by **anyone with read access to the repository**. Encrypting the plan keeps those values out of reach, and the integrity check means a tampered or swapped plan is refused.

If `PLAN_ENCRYPTION_KEY` is **not** set, tfpls still works: the plan is stored unencrypted (`plan.tgz`) and every plan comment and job log carries a warning. This is only acceptable when everyone with read access may see all secret values in your state. If that is the case for your private repository, set the workflow input `allow_unencrypted_plans: true` (default `false`) to keep the plan unencrypted **without** the warning; the artifact can then be downloaded by anyone with read access to the repository. The input never lets a public repository (or one whose visibility is unknown) save an unencrypted plan.

**Public repositories need the key.** On a public repository (or when the visibility is unknown) a plan is **not saved at all** without `PLAN_ENCRYPTION_KEY`: anyone signed in to GitHub could download the artifact, which holds the state values of the plan. The plan comment says so and how to fix it (`openssl rand -base64 32 | gh secret set PLAN_ENCRYPTION_KEY`); until then `/tofu apply` has nothing to apply. Only artifacts made by workflow runs of **this** repository are ever used, so a fork cannot plant a plan. The key is readable by every workflow of the repository, so it keeps outsiders out, not people who can push a branch.

## Create and supply the key

Create a random key once per repository (or per organisation):

```bash
openssl rand -base64 32
```

Store it as a secret named `PLAN_ENCRYPTION_KEY`:

```bash
# repository secret (prompts for the value, nothing lands in your shell history)
gh secret set PLAN_ENCRYPTION_KEY --repo OWNER/REPO

# or piped, for example straight from a password manager or openssl
openssl rand -base64 32 | gh secret set PLAN_ENCRYPTION_KEY --repo OWNER/REPO

# or one organisation secret shared by many repositories
openssl rand -base64 32 | gh secret set PLAN_ENCRYPTION_KEY --org ORG --visibility selected --repos repo1,repo2
```

Keep a copy of the key in your password manager: you need it to decrypt a plan by hand (below). The caller workflow passes it with `secrets: inherit` (the example does this), or explicitly:

```yaml
jobs:
  tofu:
    uses: denzhel/tfpls/.github/workflows/tofu.yml@v0
    secrets: inherit   # passes PLAN_ENCRYPTION_KEY and the credentials; or name them one by one:
    # secrets:
    #   TFPLS_ENV_JSON: ${{ secrets.TFPLS_ENV_JSON }}
    #   PLAN_ENCRYPTION_KEY: ${{ secrets.PLAN_ENCRYPTION_KEY }}
```

One key serves every job of the pipeline. It is only ever passed to `openssl` through an environment variable, never on a command line.

### Rotating or removing the key

Changing the key makes existing saved plans unreadable; the next apply says so and asks you to run `/tofu plan` again, which saves a new plan under the new key. That is the whole procedure. Removing the key while encrypted plans exist is refused the same way, and adding a key while only unencrypted plans exist is refused too (it could be a planted plan), so in both cases just plan again.

## Debugging and inspecting a saved plan

Everyday debugging does not change:

- the PR comment still shows the **full plan output** (and the policy result) for every workspace;
- the **run logs** are unchanged, and an apply of a reviewed plan prints the plan it is applying;
- `tofu` errors appear in the comment as before.

To inspect the saved file itself:

```bash
# 1. download the artifact (named `tfpls-plan-<workspace>-<sha12>-<targets>`; see the run's artifacts list)
gh run list --repo OWNER/REPO --workflow tofu --limit 5          # find the run
gh run download RUN_ID --repo OWNER/REPO -n tfpls-plan-terraform__prod-us-west-1-0123456789ab-all -D plan

# 2. read it with the helper from this repository (needs openssl, tar, jq; `show` also needs tofu)
export PLAN_ENCRYPTION_KEY='...'                 # from your password manager
tools/tfpls-plan decrypt plan/plan.tgz.enc ./out   # extracts tfplan + meta.json and prints the metadata
cd path/to/stack && tofu init && tools/tfpls-plan show ../../plan/plan.tgz.enc   # human-readable plan
```

Without the helper, the format is plain tools only: the file is AES-256-CBC (PBKDF2, SHA-256) over `<64 hex sha256 of the tgz>\n<tgz>`:

```bash
openssl enc -d -aes-256-cbc -pbkdf2 -md sha256 -pass env:PLAN_ENCRYPTION_KEY -in plan/plan.tgz.enc -out dec.bin
tail -c +66 dec.bin > plan.tgz && tar -xzf plan.tgz       # tfplan, meta.json
(cd path/to/stack && tofu show tfplan)
```

Unencrypted plans are just `plan.tgz`.

## Messages you may see, and what to do

| Message | Meaning | Fix |
|---|---|---|
| `no reviewed plan found (...)` | no plan was saved for this PR head commit and these targets (or the plan had no changes) | push a commit or run `/tofu plan <workspace>` and review it |
| `the reviewed plan expired on DATE` | the saved plan is older than 14 days | run `/tofu plan` again |
| `the reviewed plan was made for different code` | the branch changed or is behind the base branch since the plan | update the branch, then plan again |
| `the saved plan is encrypted but PLAN_ENCRYPTION_KEY is not set` | the apply workflow does not receive the key | pass the secret to the `tofu.yml` job (`secrets: inherit`) |
| `could not decrypt` / `failed its integrity check` | wrong key, or the artifact was altered | check the key; run `/tofu plan` again |
| `NOT encrypted but PLAN_ENCRYPTION_KEY is set` | a plain plan exists while a key is configured | run `/tofu plan` again |
| `the state changed after the plan was made` | someone applied or changed state after the plan | run `/tofu plan` again and review |
| `no reviewed plan used (...)`, re-planned | after a merge with `require_reviewed_plan: false` | informational; set it to `true` to block instead |

## Limits

- Artifacts count against repository storage; plans are small, and retention defaults to 14 days.
- The saved plan is only valid with the same tofu version and provider lock file it was made with (the version is checked; run `tofu init` with a committed `.terraform.lock.hcl`).
- Plans made from a fork are never saved or applied (fork pull requests get no credentials).
