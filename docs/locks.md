# State locks

A cancelled or crashed run can leave the state lock behind (S3 lockfiles survive a hard kill), and every later plan then fails with "Error acquiring the state lock". A lock can also belong to a laptop apply that is still running, so **nothing in this pipeline releases a lock by itself**: it shows you who holds it, and a person decides.

## Where you see a lock
- A failed plan, a drift run or an apply says `the state is locked by <who> since <time>` in its comment, drift issue and alert.
- The manual task **locks** lists every workspace with its lock state, holder, age and operation in the run's step summary:

```sh
gh workflow run tofu.yml -f task=locks            # show only, releases nothing
```

## Releasing a lock
Only when you are sure nobody is working on the workspace:

```sh
gh workflow run tofu.yml -f task=locks -f workspaces=prod-eu-west-1     # names (or labels), several separated by spaces
```

or comment `/tofu unlock <workspace>` on a pull request (needs write access). Both use the same rules: the lock must be at least 5 minutes old, and no queued or running job of this repository may be working on that workspace. Both release exactly the lock ID they found (`force-unlock` with that ID), and both are written to the [audit log](audit.md). `/tofu unlock` runs the code of the **default branch**, never of the pull request.

How the lock is found: `tofu state mv -dry-run` on an address that does not exist. It takes the lock for a moment (so it fails at once when the lock is held), moves nothing, and needs no variables or providers, so a workspace that requires variables does not make it fail.

The check needs the **apply** credentials, because releasing a lock writes to the backend; `apply_environment` can gate them like any other apply job.

## Cost
One job that checks the workspaces one after the other (checkout, tools from cache, then per workspace `tofu init` and one `state mv -dry-run`, about 20 seconds each). It runs only when you start it.

## Limits
- A lock taken by something that is not a job of this repository (a laptop, another system) is shown like any other; the active-run check cannot see it, so the 5 minute rule and your own judgement are the safety.
- With more than 300 active runs the check says it cannot tell and refuses.
