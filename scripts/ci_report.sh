#!/usr/bin/env bash
# Report the outcome of a scheduled run as one GitHub issue per label.
#
#   ci_report.sh LABEL TITLE failure|success [DETAILS]
#
# failure: open an issue with the label, or comment on the one that is already open
#          (one issue per ongoing problem, not one per night).
# success: close the open issue with the label, if any (the problem is gone).
#
# Needs GH_TOKEN (issues: write) and the GITHUB_* variables of a workflow run.
set -euo pipefail

label="$1"
title="$2"
status="$3"
details="${4:-}"

repo="${GITHUB_REPOSITORY:?}"
run_url="${GITHUB_SERVER_URL:?}/${repo}/actions/runs/${GITHUB_RUN_ID:?}"

gh label create "$label" --repo "$repo" --color B60205 \
  --description "Automated report of a failed scheduled run" 2>/dev/null || true

existing=$(gh issue list --repo "$repo" --label "$label" --state open \
  --json number --jq '.[0].number // empty')

if [ "$status" = "failure" ]; then
  body=$(printf 'Run: %s\nCommit: %s\nEvent: %s\n\n%s\n' \
    "$run_url" "${GITHUB_SHA:-unknown}" "${GITHUB_EVENT_NAME:-unknown}" "$details")
  if [ -n "$existing" ]; then
    gh issue comment "$existing" --repo "$repo" --body "Failed again.

$body"
    echo "commented on #$existing"
  else
    gh issue create --repo "$repo" --title "$title" --label "$label" --body "$body"
  fi
elif [ "$status" = "success" ]; then
  if [ -n "$existing" ]; then
    gh issue close "$existing" --repo "$repo" \
      --comment "Recovered: $run_url passed."
    echo "closed #$existing"
  else
    echo "nothing to close"
  fi
else
  echo "unknown status '$status'" >&2
  exit 2
fi
