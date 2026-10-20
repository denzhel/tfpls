#!/usr/bin/env bash
# Pick the webhook URL of a workspace's notification. The workflows pass two secrets BY NAME, never through a computed lookup:
# `secrets[<name>]` makes GitHub hand every secret of the caller to the runner (CodeQL actions/excessive-secrets-exposure).
#   DEFAULT_HOOK  secrets.SLACK_WEBHOOK, the default webhook
#   HOOKS         secrets.TFPLS_NOTIFY_WEBHOOKS, a JSON object of further webhooks: {"KEY": "https://..."}
#   KEY           the workspace's notify_secret, a key of HOOKS; empty or SLACK_WEBHOOK = DEFAULT_HOOK, else HOOKS.SLACK_WEBHOOK
# Writes the step output `url` (empty: nothing is sent), masked first: GitHub masks a secret as a whole and line by line, not a
# value inside a JSON secret, and the next step shows its inputs in the log. The secrets are read from the environment, never
# from a command line. Never fails the job: a wrong key, map or URL logs a warning and sends nothing.
set -uo pipefail
res=$(jq -nr '
  def trim: gsub("^\\s+|\\s+$"; "");
  def url: type == "string" and test("\\Ahttps?://\\S+\\z");
  ($ENV.KEY // "" | trim | if . == "" then "SLACK_WEBHOOK" else . end) as $k
  | ($k | gsub("[^A-Za-z0-9_]"; "?")) as $show
  | ($ENV.DEFAULT_HOOK // "" | trim) as $d
  | ($ENV.HOOKS // "" | trim) as $h
  | (if $h == "" then {} else ($h | try fromjson catch null) end) as $map
  | if $k == "SLACK_WEBHOOK" and $d != "" then
      if ($d | url) then [$d, "notification webhook: the secret SLACK_WEBHOOK"]
      else ["", "::warning::the secret SLACK_WEBHOOK is not an http(s) URL; no notification is sent"] end
    elif ($map | type) != "object" then
      ["", "::warning::the secret TFPLS_NOTIFY_WEBHOOKS is not a JSON object of webhook URLs ({\"KEY\": \"https://...\"}); no notification is sent for notify_secret `\($show)`"]
    elif ($map | has($k)) then
      ($map[$k] | if type == "string" then trim else . end) as $u
      | if ($u | url) then [$u, "notification webhook: key `\($show)` of the secret TFPLS_NOTIFY_WEBHOOKS"]
        else ["", "::warning::the value of `\($show)` in the secret TFPLS_NOTIFY_WEBHOOKS is not an http(s) URL; no notification is sent"] end
    elif $k == "SLACK_WEBHOOK" then ["", "no notification webhook: the secret SLACK_WEBHOOK is not set"]
    elif $h == "" then ["", "::warning::notify_secret `\($show)` names a key of the secret TFPLS_NOTIFY_WEBHOOKS, which is not set or not passed on (secrets: inherit); no notification is sent (docs/notifications.md)"]
    else ["", "::warning::notify_secret `\($show)` is not a key of the secret TFPLS_NOTIFY_WEBHOOKS; no notification is sent (docs/notifications.md)"] end
  | .[]' 2>/dev/null) || res=$'\n::warning::the notification webhook could not be picked; no notification is sent'
nl=$'\n'
url=${res%%"$nl"*}; note=${res#*"$nl"}
# the mask first (the runner unescapes %25 in command data), then one log line; the URL itself is never printed
[ -z "$url" ] || echo "::add-mask::${url//[%]/%25}"
printf '%s\n' "$note"
printf 'url=%s\n' "$url" >> "${GITHUB_OUTPUT:-/dev/null}"
exit 0
