# Apply ordering (`after`)

`triggers` plans workspaces together; `after` makes one wait for another when they are applied in the same run:

```yaml
overrides:
  "prod-*": { auto_apply: never }
  "dr-*":   { after: ["prod-*"] }     # DR is applied only once prod has succeeded
```

- It applies to a merge (`apply.yml`) and to `/tofu apply` of several workspaces. Only workspaces that are part of **the same run** are waited for: a change to `dr-*` alone does not wait for `prod-*`.
- A workspace starts its apply job (and holds its runner slot) at its turn in the matrix, which is ordered so predecessors always come first; it then polls the run until they have finished. If a predecessor failed, was cancelled or skipped, the job stops with `not applied: <workspace> has to succeed first` and nothing is applied.
- Patterns are globs on the workspace name; chains (`a` after `b` after `c`) work; a cycle is rejected when the config is read.
- Waiting counts against the job's `timeout_minutes`. A predecessor that is itself waiting for its own predecessors is fine.
- `after` orders applies only. Plans on pull requests still run in parallel.

## Staged rollouts with manual gates

A rollout "dev, then staging, then production, with a person deciding at each step" is built from three existing settings; no extra feature is needed:

```yaml
overrides:
  "dev-*":  { auto_apply: always }                              # stage 1: applied on merge
  "stg-*":  { auto_apply: never, after: ["dev-*"] }              # stage 2: applied by a person, only after dev succeeded
  "prod-*": { auto_apply: never, after: ["stg-*"] }
  "dr-*":   { auto_apply: never, after: ["prod-*"] }
```

- **Gates** are `auto_apply: never` plus the approval rules of `/tofu apply` (reviews of the current commit, [CODEOWNERS](approvals.md)). The person who comments `/tofu apply <workspace>` *is* the gate; with `apply_environment` a GitHub Environment can add required reviewers (Team plans).
- **Order** is `after:`: `/tofu apply stg-* prod-*` runs stage by stage, and a failed stage stops the later ones (above).
- The [queue](queue.md) keeps two rollouts from stepping on each other.
