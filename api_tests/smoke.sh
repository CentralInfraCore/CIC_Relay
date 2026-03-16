#!/usr/bin/env bash
# smoke.sh — CIC Relay E2E smoke tests
# Requires a running relay instance. Configure with RELAY_URL (default: http://localhost:8080).
#
# Usage:
#   RELAY_URL=http://localhost:8080 bash api_tests/smoke.sh
#
# Exit: 0 if all assertions pass, 1 on first failure.

set -euo pipefail

RELAY_URL="${RELAY_URL:-http://localhost:8080}"
PASS=0
FAIL=0

pass() { echo "  PASS: $1"; PASS=$((PASS+1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL+1)); }

assert_status() {
    local label="$1" expected="$2" actual="$3"
    if [ "$actual" = "$expected" ]; then
        pass "$label (HTTP $actual)"
    else
        fail "$label — expected HTTP $expected, got $actual"
    fi
}

assert_json_field() {
    local label="$1" field="$2" expected="$3" body="$4"
    local actual
    actual=$(echo "$body" | grep -o "\"${field}\":\"[^\"]*\"" | head -1 | sed 's/.*:"\(.*\)"/\1/' || true)
    if [ "$actual" = "$expected" ]; then
        pass "$label ($field=$actual)"
    else
        fail "$label — expected $field=$expected, got $field=$actual"
    fi
}

echo "=== CIC Relay smoke tests ==="
echo "    Target: $RELAY_URL"
echo ""

# --- GET / ---
echo "[ GET / ]"
response=$(curl -s -o /tmp/smoke_body.txt -w "%{http_code}" "$RELAY_URL/")
assert_status "GET / returns 200" "200" "$response"
body=$(cat /tmp/smoke_body.txt)
assert_json_field "GET / body has status=ok" "status" "ok" "$body"

# --- GET /healthz ---
echo "[ GET /healthz ]"
response=$(curl -s -o /tmp/smoke_body.txt -w "%{http_code}" "$RELAY_URL/healthz")
assert_status "GET /healthz returns 200" "200" "$response"
body=$(cat /tmp/smoke_body.txt)
assert_json_field "GET /healthz body has status=ok" "status" "ok" "$body"

# --- GET /readyz ---
echo "[ GET /readyz ]"
response=$(curl -s -o /tmp/smoke_body.txt -w "%{http_code}" "$RELAY_URL/readyz")
assert_status "GET /readyz returns 200" "200" "$response"
body=$(cat /tmp/smoke_body.txt)
assert_json_field "GET /readyz body has status=ok" "status" "ok" "$body"

# --- POST /set — empty body → 400 with JSON error ---
echo "[ POST /set empty body ]"
response=$(curl -s -o /tmp/smoke_body.txt -w "%{http_code}" \
    -X POST -H "Content-Type: application/json" -d '{}' "$RELAY_URL/set")
assert_status "POST /set empty body returns 400" "400" "$response"
body=$(cat /tmp/smoke_body.txt)
assert_json_field "POST /set error body has code field" "code" "invalid_request" "$body"

# --- POST /set — wrong method (GET) → 405 ---
echo "[ GET /set → 405 ]"
response=$(curl -s -o /tmp/smoke_body.txt -w "%{http_code}" "$RELAY_URL/set")
assert_status "GET /set returns 405" "405" "$response"
body=$(cat /tmp/smoke_body.txt)
assert_json_field "GET /set error body has code=method_not_allowed" "code" "method_not_allowed" "$body"

# --- Summary ---
echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
