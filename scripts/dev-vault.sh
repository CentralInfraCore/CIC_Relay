#!/usr/bin/env bash
# dev-vault.sh — local in-memory Vault for development.
#
# Starts `vault server -dev` with a fixed root token, enables Transit, and
# creates a dev signing key. No TLS, no key import, auto-unsealed.
# Intended for local development only — never use in production.
#
# Usage:
#   scripts/dev-vault.sh          — start
#   scripts/dev-vault.sh --stop   — stop
set -euo pipefail

VAULT_DEV_ROOT_TOKEN="${VAULT_DEV_ROOT_TOKEN:-dev-root-token}"
VAULT_DEV_PORT="${VAULT_DEV_PORT:-18200}"
VAULT_KEY_NAME="${VAULT_KEY_NAME:-cic-dev-sign-key}"
RUNTIME_DIR="${XDG_RUNTIME_DIR:-/tmp}"
PIDFILE="${RUNTIME_DIR}/cic-vault-dev.pid"
ENVFILE="${RUNTIME_DIR}/cic-vault-dev.env"
LOGFILE="/tmp/cic-vault-dev.log"

stop() {
  if [[ -f "$PIDFILE" ]]; then
    PID=$(cat "$PIDFILE")
    if kill -0 "$PID" 2>/dev/null; then
      kill "$PID" && echo "[*] Vault dev stopped (PID $PID)." || true
    fi
    rm -f "$PIDFILE" "$ENVFILE"
  else
    echo "[!] No PID file found — is Vault dev running?"
  fi
  exit 0
}

if [[ "${1:-}" == "--stop" || "${1:-}" == "stop" ]]; then
  stop
fi

if ! command -v vault >/dev/null 2>&1; then
  echo "[!] 'vault' binary not found. Install HashiCorp Vault: https://developer.hashicorp.com/vault/install"
  exit 1
fi

# Already running?
if [[ -f "$PIDFILE" ]]; then
  PID=$(cat "$PIDFILE")
  if kill -0 "$PID" 2>/dev/null; then
    echo "[!] Vault dev already running (PID $PID)."
    echo "    Stop with: scripts/dev-vault.sh --stop"
    exit 0
  fi
  rm -f "$PIDFILE"
fi

export VAULT_ADDR="http://127.0.0.1:${VAULT_DEV_PORT}"
export VAULT_TOKEN="${VAULT_DEV_ROOT_TOKEN}"

echo "[*] Starting Vault dev server → ${VAULT_ADDR}"
vault server -dev \
  -dev-root-token-id="${VAULT_DEV_ROOT_TOKEN}" \
  -dev-listen-address="127.0.0.1:${VAULT_DEV_PORT}" \
  > "${LOGFILE}" 2>&1 &
echo $! > "${PIDFILE}"

# Wait until Vault responds (max 5 s)
for i in $(seq 1 10); do
  if vault status -address="${VAULT_ADDR}" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

if ! vault status -address="${VAULT_ADDR}" >/dev/null 2>&1; then
  echo "[!] Vault did not start in time. Log: ${LOGFILE}"
  exit 1
fi

echo "[*] Enabling Transit secrets engine..."
vault secrets enable -address="${VAULT_ADDR}" transit 2>/dev/null || echo "    (transit already enabled)"

echo "[*] Creating dev signing key: ${VAULT_KEY_NAME}"
vault write -address="${VAULT_ADDR}" -f "transit/keys/${VAULT_KEY_NAME}" type=ecdsa-p256

# Write env file so tests/tools can source it
cat > "${ENVFILE}" <<EOF
export VAULT_ADDR="${VAULT_ADDR}"
export VAULT_TOKEN="${VAULT_DEV_ROOT_TOKEN}"
export VAULT_KEY_NAME="${VAULT_KEY_NAME}"
export VAULT_SKIP_VERIFY=1
EOF

echo ""
echo "[*] Vault dev ready."
echo "    VAULT_ADDR  = ${VAULT_ADDR}"
echo "    VAULT_TOKEN = ${VAULT_DEV_ROOT_TOKEN}"
echo "    Key         = transit/keys/${VAULT_KEY_NAME}"
echo "    PID         = $(cat "${PIDFILE}")"
echo "    Log         = ${LOGFILE}"
echo ""
echo "    Source env : source ${ENVFILE}"
echo "    Stop       : make dev-vault-stop"
echo ""