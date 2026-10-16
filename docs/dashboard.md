# Dashboard

One page that shows every workspace (in sync, drifted, failing), what is pending, the last successful apply, a link to the drift issue, and the latest activity from the [audit log](audit.md). It is built from the `tfpls-data` branch, so it needs a scheduled drift run ([drift](drift.md)) for the workspace table and `audit: true` for the activity feed.

The page is a single self-contained HTML file: no external scripts, fonts or requests, light and dark theme, usable on a phone, filters by text, status and stack, sortable columns. Data is embedded as JSON and rendered with `textContent` only, and only `https://` links are made clickable.

## Build it in a workflow

`tofu.yml` refreshes the dashboard after the scheduled drift run; start it yourself with `gh workflow run tofu.yml -f task=dashboard`. The standalone workflow `dashboard.yml` (for GitHub Pages) is optional:

```yaml
# .github/workflows/dashboard.yml in your repository
name: dashboard
on:
  workflow_dispatch:
permissions: { contents: write }
jobs:
  dashboard:
    uses: denzhel/tfpls/.github/workflows/dashboard.yml@v0
    with: { commit_page: true }
```

Every run: the page is uploaded as the **`tfpls-dashboard` artifact** (download, open `index.html`) and a status table is written to the **run summary**. With `commit_page: true` it is also stored at `dashboard/index.html` on the data branch.

## Look at it without CI

```sh
tools/tfpls-dashboard owner/repo      # builds from the tfpls-data branch with your gh login and opens it
```

This works for private repositories on any plan and needs no workflow run.

## GitHub Pages (`pages: true`)

`with: { pages: true }` deploys the page with `upload-pages-artifact` and `deploy-pages` (the caller needs `pages: write` and `id-token: write`, and Pages set to "GitHub Actions" as the source in the repository settings). **Pages on a private repository needs a paid GitHub plan** (Team, Enterprise); on the Free plan use the artifact, the run summary or `tools/tfpls-dashboard`.

Verified: the deploy worked in a test repository (made public for a few minutes): the two actions ran, the site answered `200` and showed the workspace table, filters and activity feed with the data of the last dashboard build. Things that turned up:

- **macOS self-hosted runners need GNU tar** (`brew install gnu-tar`): `upload-pages-artifact` calls `gtar` and fails with `gtar: command not found` otherwise. Linux runners have it.
- **A public repository cannot call a reusable workflow of a private repository.** If the repository that calls tfpls is public, tfpls itself must be reachable (public, or the same organisation with internal/shared access); otherwise the caller fails to start with "workflow was not found".
- The page is only as fresh as the last dashboard run; schedule the workflow or run it after the apply and drift workflows.

Access control: Pages on a private repository can be limited to people with read access to the repository (Enterprise Cloud); on a public repository the page, and therefore the workspace names, drift state and run links on it, is public. The artifact and run summary are visible only to people who can read the repository.

## Insights on the dashboard

The dashboard's "Last 30 days" block is computed from the [audit log](audit.md), nothing else is collected:

- **Applies**: applies that changed something (`applied`) and applies that failed (`error`, `policy-failed`); runs that found no changes or were held back do not count.
- **Success rate** and **applied from a reviewed plan** (the share of successful applies that used a plan reviewed on the pull request).
- **Average apply time** and the slowest apply (seconds the tofu step took, recorded as `duration_s` in the audit events).
- **Locks released by name** (manual task `locks`, `/tofu unlock`): [state locks](locks.md).
- **Busiest** workspaces by successful applies.
- **Possibly unused**: workspaces that are in sync and have had no successful apply for 90 days. It is a prompt to look, not a verdict: a quiet production workspace is fine, an abandoned dev workspace costs money. Drifted or failing workspaces are never listed.

Everything needs `audit: true` ([audit](audit.md)) to have data to show, and a scheduled drift run ([drift](drift.md)) for the workspace states.
