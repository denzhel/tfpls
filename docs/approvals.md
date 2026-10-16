# Approvals for `/tofu apply`

The pipeline does not have approval settings of its own. It follows **the rules of your repository**.

**Where the numbers come from.** A *ruleset* on the default branch (Settings, Rules, Rulesets) with a "Require a pull request before merging" rule:

| Ruleset setting | What `/tofu apply` does |
|---|---|
| Required approvals: N | needs N approving reviews from people with write access (`0` = no review needed) |
| Dismiss stale approvals when new commits are pushed | the approval must be of the **current** commit (otherwise any commit counts; a person's latest review still decides and "changes requested" blocks) |
| Require review from Code Owners | also needs an approval from the CODEOWNERS of every workspace it touches (see [CODEOWNERS](#codeowners-driven-approvals) below) |
| Require approval of the most recent reviewable push | the author and committer of the latest commit do not count as approvers |

With no such rule (or no ruleset) the pipeline asks for **one approval of the current commit**, the safe default. Rulesets are read from the API at the tip of the default branch, so a pull request cannot change them. If they cannot be read, the apply is refused. (Rulesets need a public repository or a paid plan; classic branch protection cannot be read without an admin token.)

The pipeline's own workspace rules (`policy_dir`, `policy_packs`, `policy_params`, CODEOWNERS paths) come from `.tfpls.yml` on the default branch, matched by directory and workspace. The Rego files themselves come from the pull request's checkout, so a policy change is code that review has to catch. The old `approvals:` key is an error since v0.9.0.

## Working alone, or at night: `--now`

You cannot approve your own pull request, and sometimes nobody is awake. A **repository admin** can comment

```
/tofu apply prod-us-west-1 --now
```

The approvals the repository asks for are skipped for this one apply. Everything else holds: the reviewed plan of the current commit, the policies, the up-to-date check and the apply environment. The pipeline posts a comment saying who used `--now`, and the audit log records the apply as `comment-now`, so it can be reviewed in the morning. `--now` only goes with `/tofu apply`, and only admins can use it.

For a solo repository you can instead set the ruleset's required approvals to `0`.

## Merged pull requests

A merged pull request counts as approved: the merge apply does **not** look at reviews. Whatever the repository required to let the PR merge was already enforced by GitHub, and the apply still needs `auto_apply` to allow it.

## Messages

A refusal names the workspace, the reason and the rule, for example:

> ⛔ **apply refused.** `prod`: the approval is for an older commit: new commits were pushed after the review. Ask for a new review of a97168e  _(mode `strict`, 2 required)_

When one command names several workspaces, every workspace's rule must be satisfied.

## CODEOWNERS-driven approvals

Turn on **"Require review from Code Owners"** in the pull request rule of your ruleset on the default branch (above). `/tofu apply` then asks for **one approval from an owner of every workspace the apply touches**, on top of the required number of approvals. Nothing to set in the workflow or the config.

```
# .github/CODEOWNERS (on the base branch)
/workspaces/prod-*/          @acme/sre @alice
/workspaces/dr-us-east-1/    @bob
```

- A workspace's path is its `workspace_paths` entry from `.tfpls.yml` (or the stack `dir`). The last matching CODEOWNERS rule decides the owners, same as on GitHub. A workspace with no owner has no extra requirement.
- Owners can be users and teams (`@org/team`; team members are read with the workflow token, which needs organisation read access for private teams). E-mail owners cannot be matched to reviews and are ignored.
- The approval must be for the **current head commit**; a later "request changes" from the same person cancels their approval. Commits pushed after the approval need a new review.
- **CODEOWNERS is read from the base branch**, never from the pull request, so a PR cannot change who has to approve it.
- The refusal is posted on the pull request and lists the workspace and its owners.

This is separate from GitHub's own required CODEOWNERS review on merge: use that for merging and this for `/tofu apply` (which can run before the merge).
