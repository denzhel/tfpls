#!/usr/bin/env bash
# Formatting helpers for PR comments (sourced by run.sh and the tests).

# tfpls_clip_plan <file> <max bytes>
# Prints the file; when it is longer than <max bytes> keeps the beginning and the end (the "Plan: N to add..." summary
# is at the end) with a note in between. Output is always valid UTF-8 (a cut in the middle of a character is dropped).
tfpls_clip_plan() {
  local f=$1 max=${2:-45000} size head_n tail_n
  [ -f "$f" ] || return 0
  size=$(wc -c < "$f" | tr -d ' ')
  if [ "$size" -le "$max" ]; then
    iconv -f UTF-8 -t UTF-8 -c < "$f"
    return 0
  fi
  head_n=$((max * 6 / 10)); tail_n=$((max - head_n))
  {
    head -c "$head_n" "$f"
    printf '\n\n[... %s bytes omitted: the plan is longer than a pull request comment allows; the full text is in the run logs ...]\n\n' "$((size - head_n - tail_n))"
    tail -c "$tail_n" "$f"
  } | iconv -f UTF-8 -t UTF-8 -c
}

# tfpls_strip_refresh: stdin -> stdout without the per-resource "Refreshing state...", "Reading..." and "Read complete after" lines
tfpls_strip_refresh() {
  grep -vE ': (Refreshing state\.\.\.|Reading\.\.\.|Read complete after )' || true
}

# tfpls_plan_line <file>: "N to add, N to change, N to destroy." from the plan's own "Plan: ..." line (empty when there is none)
tfpls_plan_line() {
  [ -f "$1" ] || return 0
  grep -E '^Plan: ' "$1" | tail -1 | sed -E 's/^Plan: //'
}

# tfpls_apply_line <file>: "N added, N changed, N destroyed." from the apply's own "Apply complete! Resources: ..." line (empty when there is none)
tfpls_apply_line() {
  [ -f "$1" ] || return 0
  grep -E '^Apply complete! Resources: ' "$1" | tail -1 | sed -E 's/^Apply complete! Resources: //'
}

# tfpls_apply_view: stdin (the output of tofu apply) -> stdout without the refresh/read lines and without the "Outputs:" block.
# Output values are never put into a pull request comment (a plan shows them as "known after apply"); the run log has them.
tfpls_apply_view() { tfpls_strip_refresh | sed '/^Outputs:/,$d'; }

# tfpls_comment_body <marker> <icon> <status> <label> <run_url> <plan_file> <summary> <extra> <limit>
# The sticky plan comment: marker, hidden status, summary line, heading linking to the run,
# notices, the plan in one collapsed section, and the apply hint. A clean "no changes" has no collapsed section.
tfpls_comment_body() {
  local marker=$1 icon=$2 status=$3 label=$4 url=$5 file=$6 summary=$7 extra=$8 limit=$9
  local verb=plan first="" plan_line apply_line
  [ "$status" = applied ] && verb=apply
  plan_line=$(tfpls_plan_line "$file")
  case "$status" in
    planned|policy-failed) [ -n "$plan_line" ] && first="**Plan:** $plan_line" ;;
    applied)
      apply_line=$(tfpls_apply_line "$file")
      if [ -n "$apply_line" ]; then first="**Applied:** $apply_line"
      elif [ -n "$plan_line" ]; then first="**Plan:** $plan_line"; fi ;;
  esac
  [ -n "$first" ] || first="**$summary**"
  printf '%s\n<!-- tfpls-status: %s -->\n%s\n### %s [%s](%s) %s logs\n' "$marker" "$status" "$first" "$icon" "$label" "$url" "$verb"
  [ -n "$extra" ] && printf '\n%s\n' "$extra"
  if [ "$status" != no-changes ]; then
    printf '\n<details><summary>Click here to expand</summary>\n\n```terraform\n%s\n```\n</details>\n' "$(tfpls_clip_plan "$file" "$limit")"
  fi
  # the hint only where a saved plan can be applied
  [ "$status" = planned ] && [ -z "${TFPLS_NO_HINT:-}" ] && printf '\n`/tofu apply %s` to apply' "$label"
  return 0
}

# tfpls_already_applied_body <marker> <label> <run_url> <apply_url>
# The comment of the apply after a merge when `/tofu apply` had already applied the workspace and the plan made to confirm it
# found nothing left: one line, no plan and no hint. The status stays `no-changes`, which is what readers of it saw before.
tfpls_already_applied_body() {
  printf '%s\n<!-- tfpls-status: no-changes -->\n🟢 **[%s](%s)**: Already applied by `/tofu apply` ([run](%s)); nothing changed since.\n' "$1" "$2" "$3" "$4"
}

# tfpls_unencrypted_warns <allow>: succeeds when a plan saved without a key still carries the "not encrypted" warning.
# The opt-in (allow_unencrypted_plans) only silences the warning; whether an unencrypted plan may be saved at all is decided
# by the public-repository guard in run.sh, which does not look at the opt-in.
tfpls_unencrypted_warns() { [ "${1:-false}" != true ]; }
