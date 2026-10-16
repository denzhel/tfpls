# Audit log

Every apply, drift check and unlock can append one event to an append-only log kept in your own repository, on an orphan branch (`tfpls-data` by default). Nothing leaves GitHub and nothing needs a server.

It is on by default (`audit: false` turns it off) and needs `contents: write` in the caller's permissions, which is what `tools/onboard` writes:

```yaml
jobs:
  tofu:
    uses: denzhel/tfpls/.github/workflows/tofu.yml@v0
    with: { audit: true }   # the default
    secrets: inherit
```

Plans on pull requests are not recorded: their jobs run untrusted code and stay read-only.

## Event format

One JSON object per line in `audit/YYYY-MM.jsonl`:

```json
{"ts":"2026-10-10T10:00:00Z","repo":"acme/infra","workspace":"terraform/prod-us-west-1","kind":"apply","status":"applied","actor":"alice","trigger":"issue_comment","pr":57,"sha":"1d07e53…","run":"https://github.com/acme/infra/actions/runs/1","reviewed":true,"counts":{"add":0,"change":1,"destroy":0,"replace":0},"summary":"+0 ~1 -0"}
```

`via` says how it was triggered: `auto-apply` (merge apply), `comment` (`/tofu apply`, `/tofu unlock`), `drift-remediate` / `drift-check`. `lock_id` is the state lock that was found or released (unlock events). `kind` is `apply`, `drift` or `unlock`. `status` is what the run reported (`applied`, `no-changes`, `planned`, `policy-failed`, `error`, `unlocked`, `refused` ...). `reviewed` says whether a reviewed plan was applied. `actor` is who triggered the run (the commenter for `/tofu apply`).

Read it with anything: `git fetch origin tfpls-data && git show origin/tfpls-data:audit/2026-10.jsonl | jq -c 'select(.kind=="apply")'`.

## What is, and is not, recorded

Recorded: `apply`, `drift`, `unlock`. Deliberately **not** recorded: **plans** on pull requests (their jobs run untrusted pull request code and must not write to the repository), and **dependency pull requests** (they are ordinary pull requests: their history is the pull request and the applies that follow).

## Protect the branch

The log is a branch, so anyone who may push to it may rewrite it. Add a **ruleset** (Settings → Rules) for `tfpls-data` that blocks force pushes and deletion (and restricts who can push); the workflows only ever add commits (they retry on conflict and never force-push), so they keep working. Rulesets exist on public repositories and paid plans.

## Guarantees and limits

- Writing the log never fails a deploy: if the push fails after ten attempts the run only gets a warning.
- Parallel runs do not lose events (each retries from the new branch tip).
- It is a convenience log, not tamper-proof: anyone with write access to the repository can rewrite the branch. Protect `tfpls-data` with a branch rule if you need more.
- Summaries are cut to 300 characters and never contain plan values.
