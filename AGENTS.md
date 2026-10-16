# Working on tfpls

Shared reusable GitHub Actions workflows and composite actions for OpenTofu. Read `README.md` for usage and `.github/project.json` for the tracker coordinates.

## Tracking
Work is tracked on the maintainer's project board (coordinates in `.github/project.json`) with repository issues and milestones M1-M8. Every issue has a goal, tasks, acceptance criteria and a test plan.

- Statuses: Backlog, Ready, In progress, In review, Blocked, Done. Done means the issue is closed; keep status and issue state consistent.
- For each issue: read it, move it to In progress, work on a focused branch, link the pull request, record verification evidence (run URLs of the test repository) in a comment, then move it through In review to Done.
- Do GitHub bookkeeping (status moves, fields, comments, new issues) with the `github-tracker` agent.
- Record unrelated ideas as new Backlog issues instead of widening the current one.
- The issue body is the live checklist: tick a task, acceptance criterion or test criterion (`- [x]`) only when it is proven, with the evidence in a comment or the PR Verification section; strike items that no longer apply with a reason (`- [x] ~~text~~ n/a: reason`). An issue is closed only with zero unticked boxes.
- PRs say `Closes #n` and carry a Verification section with the exact commands and outcome; the global rules in `~/.claude/CLAUDE.md` apply as well.

## Process
- Plan every task first and get the plan approved before changing anything.
- Verify workflow changes in a test repository (GitHub-hosted runners, real AWS in one region, state in S3, access through the OIDC role in the variable `TFPLS_ROLE_ARN`; the "sandbox check" of the issue templates means that repository) before closing an issue. The shared workflows are referenced by `@main`, so push to `main` before testing from there. Costs: destroy what a test created and keep NAT gateways off when idle.
- Never commit secrets or real account identifiers. Use Alpine/minimal container images.

## Triage of new issues
Run by the `Claude issue triage` workflow (`.github/workflows/claude-triage.yml`) when the owner opens an issue or someone with write access comments `/triage`; the same rules apply when a person triages by hand. Never rewrite the body of an issue someone else wrote, never close it, never touch files.

1. **Look.** Read the issue and its comments. A comment `/triage` may carry hints (`priority:p1`, `size:m`, `milestone:M3`, `area:apply`, `kind:bug`); hints win over the rules below.
2. **Kind** (label `type:*`): from the template or the title prefix (`feat:` feature, `task:`, `bug:`, `test:`, `docs:`, `spike:`), else from the content.
3. **Area** (label `area:*`): the part of the product it concerns, from the words and files named: detect, plan, apply, drift, locks, policy, dashboard, audit, deps, security, runner, docs, ci, sandbox, product.
4. **Priority** (label `priority:*`): P0 a security hole, data loss or an apply of something nobody reviewed; P1 a bug or gap that blocks real use, or is needed for the current milestone; P2 a useful improvement; P3 an idea.
5. **Size**: S under a day in one area; M a few days or two areas; L a new subsystem or one that needs an outside decision. (Written in the comment; the board column is filled from it later.)
6. **Milestone** (existing ones only, set on the issue): M3 Hardening & v1.0 for bugs, safety, security and anything needed before a release; M6 Automation for new automation; M7 Product & adoption for docs, onboarding, release and licence; M8 Ideas backlog when unsure (the default).
7. **Status** (proposed in the comment; the board sets Backlog itself): Backlog by default; Ready only if the issue has an Outcome, Tasks and Acceptance criteria; Blocked, naming what is missing, when it needs a secret, a paid plan, an account or a decision from the owner.
8. **Labels:** add the `type:`, `area:` and `priority:` labels that match; never remove a label a person set. If the issue has no template sections, add one comment with a proposed Outcome / Tasks / Acceptance skeleton.
9. **One comment to finish**, exactly one per run: `Triage: P2 / S / area:docs / M7 / Backlog. Why: <one or two lines>.` plus which hints were applied and anything unclear.
10. **Idempotent:** if the issue already has the labels and milestone, report what is there instead of changing it, unless the `/triage` comment has hints.

The project board itself (Priority, Size, Area, Kind columns) cannot be written by the workflow: an owner session fills them from the labels and the comment (the `github-tracker` agent: "sync the board").
