#!/usr/bin/env bash
# Live-Vault end-to-end driver for the feature-008 trust-flow chain.
#
# Runs INSIDE a container that has both the `vault`/openbao binary and cargo
# (see the `test-vault-live` Makefile target). It spins a throwaway `-dev-tls`
# server on loopback, provisions the PKI / cert-auth / KV mounts, then runs the
# `live-vault`-gated integration test against it. Everything is torn down on exit;
# nothing here touches a developer's real Vault, and no admin token is needed.
set -euo pipefail

WORK="$(mktemp -d)"
VAULT_PID=""
cleanup() {
	[[ -n "${VAULT_PID}" ]] && kill "${VAULT_PID}" 2>/dev/null || true
	rm -rf "${WORK}"
}
trap cleanup EXIT

export VAULT_ADDR="https://127.0.0.1:18300"
export VAULT_TOKEN="root"
export VAULT_CACERT="${WORK}/tls/vault-ca.pem"
mkdir -p "${WORK}/tls"

echo "[*] starting throwaway dev-tls OpenBao on ${VAULT_ADDR}"
vault server -dev-tls -dev-root-token-id=root \
	-dev-listen-address=127.0.0.1:18300 -dev-tls-cert-dir="${WORK}/tls" \
	>"${WORK}/server.log" 2>&1 &
VAULT_PID=$!

# Wait for it to come up (no curl dependency — use the CLI's own status).
for _ in $(seq 1 40); do
	if vault status >/dev/null 2>&1; then break; fi
	sleep 0.5
done
vault status >/dev/null 2>&1 || { echo "[!] server did not come up"; cat "${WORK}/server.log"; exit 1; }

echo "[*] provisioning PKI (self-contained dev root) + role"
vault secrets enable -path=cic-pki pki >/dev/null
vault secrets tune -max-lease-ttl=87600h cic-pki >/dev/null
vault write -field=certificate cic-pki/root/generate/internal \
	common_name="CIC Dev PKI Root" ttl=87600h key_type=ec key_bits=256 >"${WORK}/pki-root.pem"
vault write cic-pki/roles/cic-module \
	allow_any_name=true enforce_hostnames=false key_type=ec key_bits=256 max_ttl=72h >/dev/null

echo "[*] provisioning cert-auth (trusts the dev root) + scoped policy"
vault auth enable -path=cic-cert cert >/dev/null
printf 'path "cic-kv/data/module-secret" { capabilities = ["read"] }\n' \
	| vault policy write cic-module - >/dev/null
vault write auth/cic-cert/certs/cic-module \
	certificate=@"${WORK}/pki-root.pem" policies=cic-module \
	display_name=cic-module allowed_common_names="relay.worker" ttl=1h >/dev/null

echo "[*] enabling KV v2"
vault secrets enable -path=cic-kv kv-v2 >/dev/null

echo "[*] enabling Transit + ecdsa-p256 audit-chain signing key (key never leaves Vault)"
vault secrets enable -path=cic-transit transit >/dev/null
vault write cic-transit/keys/cic-audit type=ecdsa-p256 >/dev/null

echo "[*] running the live trust-flow chain + transit audit-signer tests"
export VAULT_LIVE_ADDR="${VAULT_ADDR}"
export VAULT_LIVE_TOKEN="${VAULT_TOKEN}"
export VAULT_LIVE_CACERT="${VAULT_CACERT}"
export VAULT_LIVE_PKI_MOUNT="cic-pki" VAULT_LIVE_PKI_ROLE="cic-module"
export VAULT_LIVE_CERT_MOUNT="cic-cert" VAULT_LIVE_CERT_ROLE="cic-module"
export VAULT_LIVE_KV_MOUNT="cic-kv" VAULT_LIVE_KV_PATH="module-secret"
export VAULT_LIVE_TRANSIT_MOUNT="cic-transit" VAULT_LIVE_TRANSIT_KEY="cic-audit"

cargo test -p vault-adapter --features live-vault --locked \
	--test live_chain --test transit_sign -- --nocapture
