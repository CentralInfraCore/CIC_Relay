#!/usr/bin/env bash
# Live FFI trust-flow driver: Go → Rust (cic-ffi live-vault) → real Vault → HTTP.
#
# Runs INSIDE a container that has the `vault`/openbao binary, go, and a C
# compiler (the go-builder with the vault binary mounted). The `live-vault`
# cic-ffi staticlib must already be staged at /output/lib (the rust-builder
# builds it — see the `test-ffi-live` Makefile target). It spins a throwaway
# `-dev-tls` server, provisions PKI / cert-auth / KV, runs the cgo Go test against
# it, and tears everything down. No admin token, never touches a real Vault.
set -euo pipefail

cd /git-source

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
for _ in $(seq 1 40); do
	if vault status >/dev/null 2>&1; then break; fi
	sleep 0.5
done
vault status >/dev/null 2>&1 || { echo "[!] server did not come up"; cat "${WORK}/server.log"; exit 1; }

echo "[*] provisioning PKI (self-contained dev root) + cert-auth + KV"
vault secrets enable -path=cic-pki pki >/dev/null
vault secrets tune -max-lease-ttl=87600h cic-pki >/dev/null
vault write -field=certificate cic-pki/root/generate/internal \
	common_name="CIC Dev PKI Root" ttl=87600h key_type=ec key_bits=256 >"${WORK}/pki-root.pem"
vault write cic-pki/roles/cic-module \
	allow_any_name=true enforce_hostnames=false key_type=ec key_bits=256 max_ttl=72h >/dev/null
vault auth enable -path=cic-cert cert >/dev/null
printf 'path "cic-kv/data/module-secret" { capabilities = ["read"] }\n' \
	| vault policy write cic-module - >/dev/null
vault write auth/cic-cert/certs/cic-module \
	certificate=@"${WORK}/pki-root.pem" policies=cic-module \
	display_name=cic-module allowed_common_names="relay.worker" ttl=1h >/dev/null
vault secrets enable -path=cic-kv kv-v2 >/dev/null

echo "[*] running the live FFI trust-flow test (Go → Rust → live Vault → HTTP)"
export VAULT_LIVE_ADDR="${VAULT_ADDR}"
export VAULT_LIVE_TOKEN="${VAULT_TOKEN}"
export VAULT_LIVE_CACERT="${VAULT_CACERT}"
git config --global --add safe.directory /git-source 2>/dev/null || true

# -lutil -lgcc_s are the extra native libs the live (aws-lc) staticlib needs at
# the cgo link, on top of bridge.go's `#cgo LDFLAGS` (per --print native-static-libs).
CGO_ENABLED=1 CGO_LDFLAGS="-L/output/lib -lutil -lgcc_s" \
	go test -tags ffi_cgo -count=1 -v -run TestRunFlowLive ./pkg/cicffi/...
