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

# tfpls_comment_body <marker> <icon> <status> <label> <run_url> <plan_file> <summary> <extra> <limit>
# The sticky plan comment: marker, hidden status, summary line, heading linking to the run,
# notices, the plan in one collapsed section, and the apply hint. A clean "no changes" has no collapsed section.
tfpls_comment_body() {
  local marker=$1 icon=$2 status=$3 label=$4 url=$5 file=$6 summary=$7 extra=$8 limit=$9
  local verb=plan first="" plan_line
  [ "$status" = applied ] && verb=apply
  plan_line=$(tfpls_plan_line "$file")
  case "$status" in
    planned|applied|policy-failed) [ -n "$plan_line" ] && first="**Plan:** $plan_line" ;;
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
