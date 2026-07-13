#!/usr/bin/env bash
# Live, self-contained demo of the feature-010 signed state chain.
#
# Runs INSIDE a container that has both the `vault`/openbao binary and Go (see the
# `demo` Makefile target). It spins a throwaway `-dev-tls` OpenBao on loopback,
# enables a Transit `ecdsa-p256` signing key, points the env at it, and runs the
# statechain-demo command — which records signed state transitions and verifies
# the chain, then proves it is tamper-evident. Everything is torn down on exit;
# nothing touches a developer's real Vault, and no admin token is needed.
set -euo pipefail

WORK="$(mktemp -d)"
VAULT_PID=""
cleanup() {
	[[ -n "${VAULT_PID}" ]] && kill "${VAULT_PID}" 2>/dev/null || true
	rm -rf "${WORK}"
}
trap cleanup EXIT

export VAULT_ADDR="https://127.0.0.1:18310"
export VAULT_TOKEN="root"
export VAULT_CACERT="${WORK}/tls/vault-ca.pem"
mkdir -p "${WORK}/tls"

echo "[*] starting throwaway dev-tls OpenBao on ${VAULT_ADDR}"
vault server -dev-tls -dev-root-token-id=root \
	-dev-listen-address=127.0.0.1:18310 -dev-tls-cert-dir="${WORK}/tls" \
	>"${WORK}/server.log" 2>&1 &
VAULT_PID=$!

for _ in $(seq 1 40); do
	if vault status >/dev/null 2>&1; then break; fi
	sleep 0.5
done
vault status >/dev/null 2>&1 || { echo "[!] server did not come up"; cat "${WORK}/server.log"; exit 1; }

echo "[*] enabling Transit + ecdsa-p256 signing key (key never leaves Vault)"
vault secrets enable -path=cic-transit transit >/dev/null
vault write cic-transit/keys/cic-state type=ecdsa-p256 >/dev/null

echo "[*] running the signed state-chain demo"
echo
export DEMO_TRANSIT_MOUNT="cic-transit" DEMO_TRANSIT_KEY="cic-state"
go run ./tools/statechain-demo
