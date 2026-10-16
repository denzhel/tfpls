#!/usr/bin/env bash
# Green when every needed job succeeded or was skipped (nothing to plan), red when any failed or was cancelled.
set -uo pipefail
echo "$NEEDS" | jq -r 'to_entries[] | "\(.key): \(.value.result)"'
if echo "$NEEDS" | jq -e 'all(.[]; .result == "success" or .result == "skipped")' > /dev/null; then
  echo "all plans succeeded (or nothing to plan)"
else
  echo "::error::at least one plan job failed or was cancelled"
  exit 1
fi
