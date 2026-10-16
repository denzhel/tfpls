# Queue (`queue: true`)

GitHub's concurrency groups keep **one running and one pending** run per group and silently **cancel the pending run when a third arrives**. With three merges to one workspace in a row, the middle one is lost. `queue: true` replaces that with a real first-come-first-served queue per workspace:

```yaml
jobs:
  tofu:
    uses: denzhel/tfpls/.github/workflows/tofu.yml@v0
    with: { queue: true }
    secrets: inherit
```

It works for the merge apply and for `/tofu apply` ( the PR gets a comment such as "queued: position 2" that is removed when the turn comes).

How it works: when an apply job starts it takes a numbered ticket (a file under `queue/<workspace>/` on the `tfpls-data` branch; the first push wins a number, so numbers are strictly ordered) and waits until no earlier ticket belongs to a run that is still active. It then applies and gives its ticket back. A holder that crashed, was cancelled or timed out is ignored as soon as its run is no longer active, so the queue cannot get stuck.

Things to know:

- Waiting jobs occupy a runner. On a small self-hosted pool (for example four runners) many queued applies for one workspace leave fewer runners for the rest; the wait counts against `timeout_minutes`, after which the job fails with a clear message instead of waiting forever.
- Plans (pull request plans, `/tofu plan`) are not queued; they never change anything.
- Every run applies in order, but a queued run whose plan went stale because an earlier run changed the same workspace follows the usual rules: it re-plans (or stops, with `require_reviewed_plan`).
- Without `queue`, behaviour is unchanged (GitHub's concurrency group).
- Needs `contents: write` for the tickets. If the data branch cannot be reached the job continues without waiting, with a warning.
