# Runbooks

One scenario per section: what you see, why, what to do. Commands are comments on the pull request unless stated otherwise.

## First run
What a first pull request shows and what to do. Everything is explained on the pull request itself.
- **A note that the pipeline is introduced**: GitHub runs `/tofu` comment workflows only from the default branch, so the pull request that adds `tofu.yml` plans every workspace once and `/tofu apply` works after it is merged.
- **A note that no workspaces were found**: the layout is not `workspaces/<name>/`. Run `tools/onboard` again (it writes the `stacks:` block for var-file and `envs/<name>` layouts) or see [getting started](getting-started.md#another-layout).
- **A 🔑 comment that the role could not be assumed** (STS `AccessDenied`): put the subject the comment shows into the role's trust policy (both forms, `repo:OWNER/REPO:*` and `repo:OWNER@*/REPO@*:*`), check the variable `TFPLS_ROLE_ARN`, run the job again. The comment never shows the role ARN or AWS's message; the run log has the details.
- **A warning that an override matches nothing**: the key matches a workspace name or a stack name (glob allowed); a typo is the usual cause.
- **"workspace ... does not exist: create it once with `tofu workspace new <name>`"**: the tofu workspace does not exist yet. Create it once by hand in the directory the message names (`tofu init` and `tofu workspace new <name>`, with the same backend and credentials); tfpls never creates workspaces, so a typo can never create empty state. The key `create_workspace` was removed: delete it from `.tfpls.yml`.
- **Apply refused with no one to approve**: working alone, set the ruleset's required approvals to 0, or, as a repository admin, comment `/tofu apply <workspace> --now` ([approvals](approvals.md)).

## A plan or apply fails with "Error acquiring the state lock"
**Why**: a run was cancelled or crashed while holding the lock (S3 lockfiles survive a hard kill), or another run is genuinely working.
1. Look at the lock info in the log (who, since when). If the run is still active, wait.
2. `/tofu unlock <workspace>`: it refuses a lock younger than 5 minutes and while any run is active on that workspace; otherwise it finds the lock ID itself and releases it.
3. Prevent a repeat: runs wait 2 minutes for a lock by default (`lock_timeout`, raise it for long applies); see who holds a lock with the manual task `locks` ([state locks](locks.md)).

## `/tofu apply` says "No reviewed plan to apply"
**Why**: the PR plan has not finished, failed, was made for an older commit, or its artifact expired (the message says which).
1. Wait for the `plan / <workspace>` job of the *latest* commit to finish green, or run `/tofu plan <workspace>`.
2. Apply again. After a push, the old plan is invalid by design.

## "The state changed after the plan was made"
**Why**: someone applied something else to that workspace in between; a saved plan applies only to the exact state it was made from.
1. `/tofu plan <workspace>` and review the new plan; apply that.
2. With `auto_apply` on merge this is automatic: the run plans again and applies under the same rule.

## Every plan of a workspace fails with a 403 on refresh
The plan comment says `plan failed`; the run log has `Error: reading <resource> ...: StatusCode: 403` (for example `reading EC2 Managed Prefix List (pl-...) Entries`).

**Why**: the read-only plan role can list the resource (`Describe*`) but not read it in full (`Get*`/`List*`), and a plan refreshes everything in the state, so every plan of that workspace fails until the role can read it.
1. Take the action name from the error (`GetManagedPrefixListEntries` in the example) and add it to the read-only role's policy; read actions only.
2. Push a commit or comment `/tofu plan <workspace>`.
3. Details and the role layout: [credentials](credentials.md#resource-read-is-more-than-describe).

## An apply failed half-way
The comment lists what was already changed (read from the apply output), quotes the errors and lists the options: fix forward with a new plan, or revert the change in a new pull request (the revert's plan shows exactly what it would undo). Nothing is rolled back automatically, and nothing changes infrastructure you did not review. Do not force-unlock or edit state by hand.
1. Read the quoted error. Fix forward (permissions, quota, a name clash) and `/tofu plan <workspace>`: the new plan contains only what is left.
2. Or go back: `git revert <merge-commit>` in a new pull request; its plan shows exactly what the revert would undo.
3. Never edit state by hand to "make the plan green".

## Apply refused: approval problems
- *"the approval is for an older commit"*: new commits were pushed after the review; ask for a new review of the latest commit.
- *"a reviewer requested changes"*: resolve or dismiss that review.
- *"branch is N commits behind"*: update the branch, wait for the new plan, apply.
- *"mode `basic`/`strict`, N required"*: the message ends with the rule that was applied; it comes from the pull request rule of your default-branch ruleset ([approvals](approvals.md)).
- *"CODEOWNERS approval is missing"*: the listed owner has to approve the current commit ([approvals](approvals.md)).

## Drift alert: a workspace drifted
A `Drift: <workspace>` issue is opened ([drift](drift.md)).
1. Open the latest drift run: the summary shows what differs. Look in the cloud audit trail for who changed it.
2. If the change is wanted, put it in the code (PR, plan, apply). If not, apply the code (`/tofu apply` on a PR, or `drift_remediate` for additive drift).
3. If it is expected noise, change the code or the setting that causes it, or leave the drift issue open with a comment.
The issue closes itself when the workspace is back in sync.

## A runner is offline or jobs wait forever
1. Check the runner (Settings → Actions → Runners, or the status of your own runner service).
2. A job waiting for a runner label no runner has is queued until it times out: check `runs_on` in the caller.
3. Jobs queued behind `queue: true` wait for the earlier run; look for the run in front in the queue comment/log.

## Rotating credentials
1. Create the new keys; update the secret `TFPLS_ENV_JSON` in the repository (with OIDC there is nothing to rotate).
2. Run a PR plan to check the new credentials.
3. `PLAN_ENCRYPTION_KEY`: changing it makes older saved plans unreadable (they are single-use anyway): re-plan open PRs afterwards.

## The data branch is damaged or deleted
`tfpls-data` only holds the audit log, last drift states, queue tickets and the dashboard page. Everything else keeps working without it (queue and audit continue with an empty branch; drift issues are re-opened on the next run). Restore it from a clone if you rely on the audit log.

## Roll out a new version of tfpls
Callers pin `@v0` ([releasing](releasing.md)). To try a change first, point one repository at `@main` or at an exact tag, run a PR through it, then move the pin.
