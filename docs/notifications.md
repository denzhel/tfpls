# Notifications

Applies and drift transitions can post a short message to an incoming webhook. Nothing is sent without a webhook secret.

## The default channel

Create the secret `SLACK_WEBHOOK` (a Slack incoming webhook URL) and pass secrets on with `secrets: inherit`. A successful apply looks like this:

```
Successful deployment on `prod-us-west-1` has finished
By @denzhel, <https://github.com/o/r/commit/abc|Commit Message>: `Allow GHA to assume deploy-role - ABC-123`
<https://github.com/o/r/actions/runs/123|Review Logs>
```
```
changed: aws_sqs_queue.this["jobs"]
added: aws_sqs_queue.this["dlq"]
```

| Run | Message |
|---|---|
| apply that changed something | the three lines above and one `<action>: <address>` line per changed resource (`added`, `changed`, `deleted`, `replaced`), each resource once, at most 50, then `… N more, see Review Logs`; addresses only, never values |
| failed apply | `A run on `ws` has failed`, the same two lines, the error summary, and your `notify_mention` in front |
| apply with no changes | nothing is posted |
| `/tofu apply` on a pull request | the commenter and the pull request title and link instead of the commit |
| drift (transition) / recovery | `Drift detected on `ws`` (with the mention) / `Drift on `ws` is resolved` |

`By @…` is the author of the pushed commit (falling back to the person who started the run); for a scheduled run there is no commit message and the line has only the name.

## Your own message: `notify_template`

Point a workspace at a [jq](https://jqlang.github.io/jq/manual/) file in the repository and it builds the webhook body from the **run JSON**:

```yaml
overrides:
  "prod-*": { notify_template: .github/tfpls/slack.jq }
```

```jq
# .github/tfpls/slack.jq: nothing for successes, a short line for everything else
if .ok then empty else
  {text: "\(.mention)`\(.workspace)` \(.status): \(.summary) <\(.run_url)|logs>"}
end
```

The run JSON:

| Field | Meaning |
|---|---|
| `repo`, `workspace` | `owner/name`, the workspace label |
| `kind` | `apply`, `drift` or `unlock` |
| `status` | `applied`, `no-changes`, `planned`, `skipped`, `error`, `policy-failed`, `drift`, `in-sync`, `unlocked` |
| `ok` | true unless something needs attention (`error`, `policy-failed`, `drift`) |
| `summary` | short text (500 characters at most), no plan values |
| `counts` | `{add, change, destroy, replace}` or null |
| `changes`, `changes_more` | `[{action, address}]` (50 at most) and how many more there were |
| `author`, `commit` | the person, `{sha, message (first line), url}` |
| `pr` | the pull request number for `/tofu apply`, else null |
| `run_url`, `mention` | link to the run; your `notify_mention` text (with a trailing space) on failures only |

Examples:

```jq
# plain text, one line, and only when something changed or went wrong
if .status == "no-changes" then empty
else {text: "\(.mention)\(.repo) `\(.workspace)`: \(.status) (\(.counts // {} | "\(.add // 0)+ \(.change // 0)~ \(.destroy // 0)-")) <\(.run_url)|logs>"} end
```

```jq
# Block Kit with a status colour (an attachment), nothing for successes
if .ok then empty else
{attachments: [{
  color: "#d9534f",
  blocks: [
    {type: "section", text: {type: "mrkdwn", text: "\(.mention)*\(.workspace)* \(.status)\n\(.summary)"}},
    {type: "context", elements: [{type: "mrkdwn", text: "By @\(.author) · <\(.run_url)|Review Logs>"}]}
  ]
}]}
end
```

Rules:
- The template's output is posted **as is** (for every `notify_format`), so it must be one JSON object, e.g. `{text: ...}` for Slack or `{blocks: [...]}` for Block Kit. **No output (`empty`) sends nothing.**
- It runs with `jq` and an **empty environment**: `$ENV` and `env` are empty, nothing secret is reachable.
- A template that fails, prints something that is not one JSON object, or is missing logs a warning and the **default message is sent**; the job never fails.
- For `/tofu apply` the template is read from the **default branch**, so a pull request cannot change or silence the message about its own apply.
- The path is relative to the repository root (no `/`, no `..`).

## Per workspace: another channel, another system, a mention

A Slack incoming webhook belongs to one channel, so "another channel" means "another webhook". Put the other webhooks into **one** secret, `TFPLS_NOTIFY_WEBHOOKS`: a JSON object from a key to a webhook URL.

```json
{
  "SLACK_WEBHOOK_PLATFORM": "https://hooks.slack.com/services/…",
  "SLACK_WEBHOOK_ONCALL": "https://hooks.slack.com/services/…",
  "TEAMS_WEBHOOK": "https://….webhook.office.com/…"
}
```

Save it with `gh secret set TFPLS_NOTIFY_WEBHOOKS < webhooks.json` (then delete the file) and point workspaces at a **key**:

```yaml
defaults:
  notify_secret: SLACK_WEBHOOK_PLATFORM      # a KEY of TFPLS_NOTIFY_WEBHOOKS (capital letters, digits, _), never the URL
overrides:
  "prod-*":
    notify_secret: SLACK_WEBHOOK_ONCALL
    notify_mention: "<!subteam^S0123456>"    # a Slack user group; added only to failures and drift
  "dr-*":
    notify_secret: TEAMS_WEBHOOK
    notify_format: teams
```

| `notify_format` | Payload |
|---|---|
| `slack` (default) | `{"text": "..."}` |
| `teams` | a MessageCard (`@type: MessageCard`) for Microsoft Teams incoming webhooks / connectors |
| `generic` | `{"repo","workspace","kind","status","ok","summary","url","author","commit","changes","changes_more"}` for your own receiver (PagerDuty bridge, chat bot, Zapier …) |

The Teams and generic formats keep posting every status (including `no-changes`); only the default Slack message skips runs without changes. The mention is added only when the status is a failure or drift, never for successes. Summaries are cut to 500 characters and contain no plan values. A failing webhook never fails a run (it logs a warning).

How the webhook is chosen:
- No `notify_secret`, or `SLACK_WEBHOOK`: the secret `SLACK_WEBHOOK`; when that is not set, the key `SLACK_WEBHOOK` of `TFPLS_NOTIFY_WEBHOOKS`; when neither exists, nothing is sent.
- Any other `notify_secret`: that key of `TFPLS_NOTIFY_WEBHOOKS`. A key that is not there, a secret that is not set or is not a JSON object, or a value that is not an `http(s)://` URL logs a warning and sends nothing; the run never fails.
- The URL that is used is masked in the log: GitHub masks a secret as a whole (and line by line), not each value inside a JSON secret.
- The caller passes both secrets on with `secrets: inherit`, or lists them ([reference.md](reference.md)).

Why one secret and not one secret per workspace: picking a secret by a computed name (`secrets[name]`) makes GitHub hand **every** secret the workflow can see to the job, and with `secrets: inherit` that is every repository and organisation secret of the caller, in the jobs that run OpenTofu (and, for `/tofu plan`, the code of a pull request). The workflows name the two webhook secrets instead, so those are all a job gets for notifications ([security model](security-model.md#what-the-jobs-can-reach)).

**Coming from a secret per workspace (v0.3.0 and earlier).** `notify_secret` was the name of a secret of its own. Copy each of those secrets into `TFPLS_NOTIFY_WEBHOOKS` under the same name (`{"SLACK_WEBHOOK_ONCALL": "<its URL>", ...}`); the config stays as it is; then delete the old secrets. Until then such a workspace sends nothing, and its run log warns.

[Drift](drift.md) messages are sent only when a workspace changes state, not on every run.

## Verified

Slack (first version of the message, before the current layout): five real messages were accepted by a Slack incoming webhook in a test repository: a successful apply (`applied`), a failed apply (`error`), a drift alert (`drift`) and the two recoveries (`in-sync`) from a drift run with `drift_issues: true`. Each run log says `slack notification sent for <workspace> (<status>)` when the webhook accepted the message and warns `notification failed` otherwise. Mentions (`notify_mention`) and the Teams and generic formats were exercised against a local receiver and unit tests, not a real Teams/Slack mention.

The current layout was checked in a test repository with the same kind of webhook: a default success message after a merge apply, a failure message (an apply without a reviewed plan, with the author, the pull request and the error), a custom template (`{"text": "custom: ..."}` posted exactly as written) and runs without changes that sent nothing. The run log of every notification contains the message, so a run can be checked without Slack. Not exercised live: the mention on a real failure, drift messages in the new layout, and a template with a jq syntax error (unit tests only).

The choice of the webhook (the default, a key of `TFPLS_NOTIFY_WEBHOOKS`, the warnings and the masking) is covered by `tests/finish-webhook.test.sh`; a webhook taken from `TFPLS_NOTIFY_WEBHOOKS` has not been exercised live yet.
