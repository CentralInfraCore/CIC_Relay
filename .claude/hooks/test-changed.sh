#!/usr/bin/env bash
# Runs go test -race on packages with changed Go files since HEAD.
# Exits 0 if all pass or nothing changed.
# Exits 2 (asyncRewake) if any test fails — Claude is woken to fix it.
set -euo pipefail

REPO=/home/sinkog/sync/git.partners/CentralInfraCore/CIC-Relay
cd "$REPO"

# Changed Go files vs HEAD (tracked changes + untracked new files)
CHANGED_DIRS=$(
  {
    git diff HEAD --name-only -- '*.go' 2>/dev/null
    git ls-files --others --exclude-standard -- '*.go' 2>/dev/null
  } | sed 's|/[^/]*\.go$||' | sort -u
)

if [ -z "$CHANGED_DIRS" ]; then
  exit 0
fi

# Map directory paths to fully-qualified Go package paths
PKGS=$(echo "$CHANGED_DIRS" | awk '{print "centralrelay/" $0}' | tr '\n' ' ')

# Run tests inside the Go builder container (read-only mount, Go available)
RESULT=$(docker compose exec -T go-builder sh -c \
  "cd /git-source && \
   git config --global --add safe.directory /git-source 2>/dev/null || true; \
   GOFLAGS='-mod=readonly -trimpath' go test -race $PKGS 2>&1") \
  && RC=0 || RC=$?

if [ "$RC" -ne 0 ]; then
  # Escape for JSON string value
  ESCAPED=$(printf '%s' "$RESULT" | python3 -c "import sys,json; print(json.dumps(sys.stdin.read()))")
  printf '{"hookSpecificOutput":{"hookEventName":"Stop","additionalContext":"Go test failures in: %s\n%s"}}' \
    "$PKGS" "${ESCAPED//\"/\\\"}"
  exit 2
fi

exit 0
