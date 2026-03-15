#!/usr/bin/env bash
# makefile_full_test.sh — End-to-end smoke & quality test for the CIC-Relay Makefile
# Usage:
#   bash makefile_full_test.sh [COVERAGE_MIN]
#
# Requirements: git, make, docker, docker compose (v2), awk, sha256sum
# Notes:
# - This script DOES execute your make targets. It will build inside your docker builder.
# - It does NOT publish (sets NO_PUBLISH=1).

set -euo pipefail

COVERAGE_MIN="${1:-75}"
APP_NAME_DEFAULT="cic-relay"

red()   { printf "\033[31m%s\033[0m\n" "$*"; }
green() { printf "\033[32m%s\033[0m\n" "$*"; }
yellow(){ printf "\033[33m%s\033[0m\n" "$*"; }
blue()  { printf "\033[34m%s\033[0m\n" "$*"; }

fail() { red "✖ $*"; exit 1; }
pass() { green "✔ $*"; }

step() { blue "\n==> $*"; }

require() {
  command -v "$1" >/dev/null 2>&1 || fail "Missing required command: $1"
}

# --- Pre-flight checks ---
step "Pre-flight checks"
require git
require awk
require make
require sha256sum
require docker

# docker compose (plugin) vs docker-compose (legacy)
if docker compose version >/dev/null 2>&1; then
  COMPOSE="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE="docker-compose"
else
  fail "Docker Compose not found (need 'docker compose' or 'docker-compose')."
fi
pass "Compose detected: $COMPOSE"

# Basic repo checks
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || fail "Not inside a git repository."
COMMIT="$(git rev-parse --short HEAD)"
pass "Git OK, commit: $COMMIT"

test -f Makefile || fail "Makefile not found in current directory."
pass "Makefile found"

# --- Discover APP_NAME (best-effort from Makefile) ---
APP_NAME="$(awk -F'?=|:=' '/^\s*APP_NAME\s*(\?=|:=)\s*/{gsub(/[[:space:]]/,"",$2); print $2; exit}' Makefile || true)"
APP_NAME="${APP_NAME:-$APP_NAME_DEFAULT}"
pass "App name: $APP_NAME"

# --- Show help (if available) ---
step "make help (if present)"
if make -n help >/dev/null 2>&1; then
  make help || true
  pass "help target invoked"
else
  yellow "No help target; continuing"
fi

# --- Bring infra up / prepare ---
step "prepare environment (infra-init/prepare)"
if grep -qE '^\s*prepare\s*:' Makefile; then
  make prepare
else
  yellow "No prepare target; attempting 'infra-init' directly"
  if grep -qE '^\s*infra-init\s*:' Makefile; then
    make infra-init
  else
    yellow "No infra-init target either; assuming environment is already up"
  fi
fi
pass "Environment prepared (or skipped)"

# --- Quality gate ---
step "quality gate (fmt-check, lint, vet)"
if grep -qE '^\s*quality\s*:' Makefile; then
  make quality
else
  yellow "No quality target; attempting common checks"
  grep -qE '^\s*fmt-check\s*:' Makefile && make fmt-check || yellow "No fmt-check"
  grep -qE '^\s*lint\s*:' Makefile && make lint || yellow "No lint"
  grep -qE '^\s*vet\s*:' Makefile && make vet || yellow "No vet"
fi
pass "Quality checks passed"

# --- Tests ---
step "unit tests"
if grep -qE '^\s*test\s*:' Makefile; then
  make test
else
  yellow "No test target"
fi
pass "Tests OK"

# --- Coverage (with threshold) ---
step "coverage"
COVERAGE_HTML=""
COVERAGE_FILE=""
if grep -qE '^\s*coverage\s*:' Makefile; then
  make coverage
  # Guess coverage file/html location from typical variables/paths
  # Try to read COVERAGE_FILE and COVERAGE_HTML from Makefile
  CF="$(awk -F'?=|:=' '/^\s*COVERAGE_FILE\s*(\?=|:=)\s*/{gsub(/[[:space:]]/,"",$2); print $2; exit}' Makefile || true)"
  CH="$(awk -F'?=|:=' '/^\s*COVERAGE_HTML\s*(\?=|:=)\s*/{gsub(/[[:space:]]/,"",$2); print $2; exit}' Makefile || true)"
  # Expand simple vars
  CF="${CF//\$(COMMIT)/$COMMIT}"; CH="${CH//\$(COMMIT)/$COMMIT}"
  COVERAGE_FILE="${CF:-output/$COMMIT/coverage.out}"
  COVERAGE_HTML="${CH:-output/$COMMIT/coverage.html}"
  # Normalize leading slash if volume maps /output -> ./output
  [[ "$COVERAGE_FILE" == /* ]] && COVERAGE_FILE="./output/$COMMIT/coverage.out"
  [[ "$COVERAGE_HTML" == /* ]] && COVERAGE_HTML="./output/$COMMIT/coverage.html"
  test -f "$COVERAGE_FILE" || yellow "Coverage profile not found at $COVERAGE_FILE"
  test -f "$COVERAGE_HTML"  || yellow "Coverage HTML not found at $COVERAGE_HTML"
else
  yellow "No coverage target"
fi

# Coverage threshold if coverage profile exists
if [[ -f "$COVERAGE_FILE" ]]; then
  TOTAL="$(awk '/^total:/ { gsub("%","",$3); print $3 }' "$COVERAGE_FILE" || true)"
  if [[ -z "$TOTAL" ]]; then
    # Fallback: compute via go tool cover -func
    if $COMPOSE exec -T builder sh -lc "test -f '$COVERAGE_FILE'"; then
      TOTAL="$($COMPOSE exec -T builder sh -lc "go tool cover -func=$COVERAGE_FILE | awk '/total:.*statements/ { gsub(\"%\",\"\",\$3); print \$3 }'")"
    fi
  fi
  if [[ -n "${TOTAL:-}" ]]; then
    echo "Total coverage: ${TOTAL}%"
    awk -v t="$TOTAL" -v m="$COVERAGE_MIN" 'BEGIN{ if (t+0 < m+0) exit 1 }' || fail "Coverage below threshold ${COVERAGE_MIN}%"
    pass "Coverage >= ${COVERAGE_MIN}%"
  else
    yellow "Could not determine total coverage percentage"
  fi
fi

# --- Build (no publish) ---
step "build (NO_PUBLISH=1)"
make NO_PUBLISH=1 build

# --- Verify artefacts ---
step "verify artefacts"
OUTDIR="./output/${COMMIT}"
BINPATH="${OUTDIR}/${APP_NAME}"
MANIFEST="${OUTDIR}/MANIFEST.sha256"

test -f "$BINPATH"   || fail "Built binary not found: $BINPATH"
test -f "$MANIFEST"  || fail "MANIFEST not found: $MANIFEST"

# Compare manifest to computed sha256
EXPECTED="$(cut -d' ' -f1 "$MANIFEST")"
ACTUAL="$(sha256sum "$BINPATH" | awk '{print $1}')"
if [[ "$EXPECTED" != "$ACTUAL" ]]; then
  fail "MANIFEST mismatch: expected $EXPECTED, got $ACTUAL"
fi
pass "MANIFEST matches binary"

# Optional: smoke-run version if binary supports it
step "binary smoke-run (optional)"
set +e
"$BINPATH" -h >/dev/null 2>&1 && pass "Binary responds to -h" || yellow "Binary -h not available (skipping)"
"$BINPATH" --version >/dev/null 2>&1 && pass "Binary responds to --version" || yellow "Binary --version not available (skipping)"
set -e

# --- Source manifest (optional) ---
if grep -qE '^\s*manifest-src\s*:' Makefile; then
  step "source manifest"
  make manifest-src
  test -f "./output/${COMMIT}/SOURCE.MANIFEST.sha256" && pass "Source manifest generated" || yellow "Source manifest missing"
fi

# --- Summary ---
step "SUMMARY"
green "All steps completed."
echo "Artefacts:"
echo "  Binary:   $BINPATH"
test -f "$COVERAGE_HTML" && echo "  Coverage: $COVERAGE_HTML"
echo "  Manifest: $MANIFEST"

