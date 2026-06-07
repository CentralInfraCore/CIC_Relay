#!/usr/bin/env bash
set -euo pipefail

# bootstrap_testdata.sh — creates the expected cmd/relay/testdata structure with sample PKI, signed artifacts,
# negatives, and golden/manifests.
# Run from repo root:  bash tools/bootstrap_testdata.sh

ROOT="cmd/relay/testdata"

# --- helpers -----------------------------------------------------------------
msg(){ printf "\033[1;32m==>\033[0m %s\n" "$*"; }
err(){ printf "\033[1;31m[err]\033[0m %s\n" "$*" >&2; }

# --- dependency checks -----------------------------------------------------
for cmd in openssl sha256sum xxd faketime; do
  if ! command -v "$cmd" &> /dev/null; then
    err "Required command '$cmd' not found. Please install it."
    exit 1
  fi
done

mkd(){ mkdir -p "$@"; }

# openssl config (minimal) via heredocs for Root CA and Company CA
make_root_ca(){
  local dir="$1"
  mkd "$dir"
  cat >"$dir/openssl.cnf" <<'EOF'
[ req ]
prompt = no
distinguished_name = dn
x509_extensions = v3_ca
[ dn ]
C = HU
O = Example Root CA
CN = Example Root CA (current)
[ v3_ca ]
subjectKeyIdentifier=hash
authorityKeyIdentifier=keyid:always,issuer
basicConstraints = critical, CA:true, pathlen:2
keyUsage = critical, keyCertSign, cRLSign
EOF
  openssl genrsa -out "$dir/root-ca.key" 4096 >/dev/null 2>&1
  openssl req -x509 -new -days 365 -key "$dir/root-ca.key" -out "$dir/root-ca.pem" -config "$dir/openssl.cnf" >/dev/null 2>&1

  # next-year root (dual trust window demo)
  sed 's/(current)/(next)/' "$dir/openssl.cnf" >"$dir/openssl.next.cnf"
  openssl genrsa -out "$dir/root-ca.next.key" 4096 >/dev/null 2>&1
  openssl req -x509 -new -days 730 -key "$dir/root-ca.next.key" -out "$dir/root-ca.next.pem" -config "$dir/openssl.next.cnf" >/dev/null 2>&1
}

make_company_ca(){
  local dir="$1"; local rootdir="$2"
  mkd "$dir"
  cat >"$dir/ca.cnf" <<'EOF'
[ req ]
prompt = no
distinguished_name = dn
[ dn ]
C = HU
O = Example Company
CN = Example Company CA
[ v3_intermediate ]
subjectKeyIdentifier=hash
authorityKeyIdentifier=keyid:always,issuer
basicConstraints = critical, CA:true, pathlen:1
keyUsage = critical, keyCertSign, cRLSign
EOF
  openssl genrsa -out "$dir/company-ca.key" 4096 >/dev/null 2>&1
  openssl req -new -key "$dir/company-ca.key" -out "$dir/company-ca.csr" -config "$dir/ca.cnf" >/dev/null 2>&1
  openssl x509 -req -in "$dir/company-ca.csr" -CA "$rootdir/root-ca.pem" -CAkey "$rootdir/root-ca.key" -CAcreateserial \
    -days 365 -extfile "$dir/ca.cnf" -extensions v3_intermediate -out "$dir/company-ca.pem" >/dev/null 2>&1
}

# Generic EE cert maker
# $1: outdir, $2: CN, $3: usages (comma list: codeSigning,serverAuth,clientAuth), $4: ca_pem, $5: ca_key
make_ee(){
  local dir="$1"; local cn="$2"; local usages="$3"; local ca_pem="$4"; local ca_key="$5"
  mkd "$dir"
  cat >"$dir/ee.cnf" <<EOF
[ req ]
prompt = no
distinguished_name = dn
req_extensions = v3_req_ext

[ dn ]
C = HU
O = Example Org
CN = ${cn}

[ v3_req_ext ]
subjectKeyIdentifier = hash

[ v3_ee_ext ]
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always,issuer
basicConstraints = CA:false
keyUsage = critical, digitalSignature
extendedKeyUsage = ${usages}
subjectAltName = email:${cn//@/}
EOF
  openssl genrsa -out "$dir/ee.key" 3072 >/dev/null 2>&1
  openssl req -new -key "$dir/ee.key" -out "$dir/ee.csr" -config "$dir/ee.cnf" >/dev/null 2>&1
  openssl x509 -req -in "$dir/ee.csr" -CA "$ca_pem" -CAkey "$ca_key" -CAcreateserial -days 365 \
    -extfile "$dir/ee.cnf" -extensions v3_ee_ext -out "$dir/ee.pem" >/dev/null 2>&1
}

# Expired EE (for negatives)
make_expired_ee(){
  local dir="$1"; local cn="$2"; local usages="$3"; local ca_pem="$4"; local ca_key="$5"
  mkd "$dir"
  cat >"$dir/ee.cnf" <<EOF
[ req ]
prompt = no
distinguished_name = dn
req_extensions = v3_req_ext

[ dn ]
C = HU
O = Example Org
CN = ${cn}

[ v3_req_ext ]
subjectKeyIdentifier = hash

[ v3_ee_ext ]
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always,issuer
basicConstraints = CA:false
keyUsage = critical, digitalSignature
extendedKeyUsage = ${usages}
EOF
  openssl genrsa -out "$dir/ee.key" 3072 >/dev/null 2>&1
  openssl req -new -key "$dir/ee.key" -out "$dir/ee.csr" -config "$dir/ee.cnf" >/dev/null 2>&1
  # Create an already-expired cert by faking the system time
  faketime '2023-01-01' openssl x509 -req -in "$dir/ee.csr" -CA "$ca_pem" -CAkey "$ca_key" -CAcreateserial \
    -days  365\
    -extfile "$dir/ee.cnf" -extensions v3_ee_ext -out "$dir/ee.pem" >/dev/null 2>&1
}

cms_sign(){ # $1 input file, $2 out sig, $3 signer pem, $4 signer key, $5 chain pem (for -certfile)
  openssl cms -sign -in "$1" -out "$2" -signer "$3" -inkey "$4" -certfile "$5" -outform PEM -nodetach >/dev/null 2>&1 || {
    err "CMS sign failed for $1"; return 1; }
}

sha256_file(){ sha256sum "$1" | awk '{print $1}'; }

# --- layout ------------------------------------------------------------------
msg "Creating directories"
rm -rf "$ROOT"
mkd "$ROOT/pki/trust/companies"
mkd "$ROOT/pki/signers/"{developers,companies,sources,infras}
mkd "$ROOT/pki/chains"
mkd "$ROOT/minimal/"{schemas,modules,workflows,wasm,golden/{wasm,json},json,manifest}
mkd "$ROOT/invalid/"{wrong-policy,wrong-eku,wrong-chain,expired,oversize,too-large-request}

# --- PKI ---------------------------------------------------------------------
msg "Generating PKI"
make_root_ca        "$ROOT/pki/trust"
make_company_ca     "$ROOT/pki/trust/companies" "$ROOT/pki/trust"
cp "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/companies/example-company-ca.pem"

# signers
# sources: module/schema/workflow — codeSigning
make_ee "$ROOT/pki/signers/sources/module"   "module-signer@example"   "codeSigning" "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/companies/company-ca.key"
make_ee "$ROOT/pki/signers/sources/schema"   "schema-signer@example"   "codeSigning" "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/companies/company-ca.key"
make_ee "$ROOT/pki/signers/sources/workflow" "workflow-signer@example" "codeSigning" "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/companies/company-ca.key"

# infras: relay (codeSigning), infra-module (codeSigning), host-local (serverAuth)
make_ee "$ROOT/pki/signers/infras/relay"       "relay-signer@example"       "codeSigning" "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/companies/company-ca.key"
make_ee "$ROOT/pki/signers/infras/infra-module" "infra-module-signer@example" "codeSigning" "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/companies/company-ca.key"
make_ee "$ROOT/pki/signers/infras/host-local"  "host-local@example"         "serverAuth" "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/companies/company-ca.key"

# companies/example-company-signer (codeSigning)
make_ee "$ROOT/pki/signers/companies/example" "example-company-signer@example" "codeSigning" "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/companies/company-ca.key"

# developers/dev-alice-ee (codeSigning)
make_ee "$ROOT/pki/signers/developers/alice" "dev-alice-ee@example" "codeSigning" "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/companies/company-ca.key"

# Publish public EE certs in flat locations expected by tree
cp "$ROOT/pki/signers/developers/alice/ee.pem"        "$ROOT/pki/signers/developers/dev-alice-ee.pem"
cp "$ROOT/pki/signers/companies/example/ee.pem"       "$ROOT/pki/signers/companies/example-company-signer.pem"
cp "$ROOT/pki/signers/sources/module/ee.pem"          "$ROOT/pki/signers/sources/module-signer.pem"
cp "$ROOT/pki/signers/sources/schema/ee.pem"          "$ROOT/pki/signers/sources/schema-signer.pem"
cp "$ROOT/pki/signers/sources/workflow/ee.pem"        "$ROOT/pki/signers/sources/workflow-signer.pem"
cp "$ROOT/pki/signers/infras/host-local/ee.pem"       "$ROOT/pki/signers/infras/host-local.pem"
cp "$ROOT/pki/signers/infras/infra-module/ee.pem"     "$ROOT/pki/signers/infras/infra-module-signer.pem"
cp "$ROOT/pki/signers/infras/relay/ee.pem"            "$ROOT/pki/signers/infras/relay-signer.pem"

# Chains (EE -> Company -> Root)
for x in source-module source-schema source-workflow infra-relay infra-host ; do :; done
cat "$ROOT/pki/signers/sources/module/ee.pem"   "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/root-ca.pem" > "$ROOT/pki/chains/source-module.chain.pem"
cat "$ROOT/pki/signers/sources/schema/ee.pem"   "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/root-ca.pem" > "$ROOT/pki/chains/source-schema.chain.pem"
cat "$ROOT/pki/signers/sources/workflow/ee.pem" "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/root-ca.pem" > "$ROOT/pki/chains/source-workflow.chain.pem"
cat "$ROOT/pki/signers/infras/relay/ee.pem"     "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/root-ca.pem" > "$ROOT/pki/chains/infra-relay.chain.pem"
cat "$ROOT/pki/signers/infras/host-local/ee.pem" "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/root-ca.pem" > "$ROOT/pki/chains/infra-host.chain.pem"

# --- minimal (happy path) ----------------------------------------------------
msg "Creating minimal happy-path artifacts"
cat >"$ROOT/minimal/schemas/user.schema.yaml" <<'YAML'
apiVersion: relay.cic.com/v1
kind: Schema
metadata:
  name: user.schema
  version: "1.0"
spec:
  type: object
  properties:
    id:
      type: string
    email:
      type: string
YAML

cat >"$ROOT/minimal/modules/user.module.yaml" <<'YAML'
apiVersion: v1
kind: Module
metadata:
  name: user
spec:
  schema: user
  wasm: wasm/add.wasm
YAML

cat >"$ROOT/minimal/workflows/order.workflow.yaml" <<'YAML'
apiVersion: relay.cic.com/v1
kind: Workflow
metadata:
  name: order.workflow
  version: "1.0"
spec:
  steps:
    - "run: user with: {id: '42', email: 'demo@example.com'}"
YAML

# tiny placeholder WASM (not a real module, but has magic header). Replace with real sample if needed.
printf '\x00asm\x01\x00\x00\x00' >"$ROOT/minimal/wasm/add.wasm"

# sign all happy-path sources with the proper signers
cms_sign "$ROOT/minimal/schemas/user.schema.yaml"   "$ROOT/minimal/schemas/user.schema.yaml.sig"   \
  "$ROOT/pki/signers/sources/schema/ee.pem"   "$ROOT/pki/signers/sources/schema/ee.key"   "$ROOT/pki/trust/companies/company-ca.pem"
cms_sign "$ROOT/minimal/modules/user.module.yaml"   "$ROOT/minimal/modules/user.module.yaml.sig"   \
  "$ROOT/pki/signers/sources/module/ee.pem"   "$ROOT/pki/signers/sources/module/ee.key"   "$ROOT/pki/trust/companies/company-ca.pem"
cms_sign "$ROOT/minimal/workflows/order.workflow.yaml" "$ROOT/minimal/workflows/order.workflow.yaml.sig" \
  "$ROOT/pki/signers/sources/workflow/ee.pem" "$ROOT/pki/signers/sources/workflow/ee.key" "$ROOT/pki/trust/companies/company-ca.pem"
cms_sign "$ROOT/minimal/wasm/add.wasm" "$ROOT/minimal/wasm/add.wasm.sig" \
  "$ROOT/pki/signers/infras/infra-module/ee.pem" "$ROOT/pki/signers/infras/infra-module/ee.key" "$ROOT/pki/trust/companies/company-ca.pem"

# golden placeholders
xxd -p "$ROOT/minimal/wasm/add.wasm" | tr -d '\n' >"$ROOT/minimal/golden/wasm/add.canonical.hex"
cat >"$ROOT/minimal/golden/json/order.workflow.canon.json" <<'JSON'
{"apiVersion":"v1","kind":"Workflow","metadata":{"name":"order"},"steps":[{"run":"user","with":{"email":"demo@example.com","id":"42"}}]}
JSON

# manifest + filelist
(
  cd "$ROOT/minimal"
  find . -type f | sort > manifest/FILELIST.csv
  while IFS= read -r f; do
    printf "%s  %s\n" "$(sha256_file "$f")" "$f"
  done < manifest/FILELIST.csv > manifest/MANIFEST.sha256
)

# --- invalid profiles --------------------------------------------------------
msg "Creating invalid profiles"
# wrong-policy: just reuse a valid signature but rename to indicate policy mismatch scenario
cp "$ROOT/minimal/modules/user.module.yaml.sig" "$ROOT/invalid/wrong-policy/user.module.yaml.sig"
# wrong-eku: create a signer without codeSigning (clientAuth only) and sign workflow
make_ee "$ROOT/pki/signers/negatives/client-only" "client-only@example" "clientAuth" "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/companies/company-ca.key"
cms_sign "$ROOT/minimal/workflows/order.workflow.yaml" "$ROOT/invalid/wrong-eku/order.workflow.yaml.sig" \
  "$ROOT/pki/signers/negatives/client-only/ee.pem" "$ROOT/pki/signers/negatives/client-only/ee.key" "$ROOT/pki/trust/companies/company-ca.pem"
# wrong-chain: sign schema with company signer but provide only EE cert in chain (omit CA/Root)
cp "$ROOT/minimal/schemas/user.schema.yaml.sig" "$ROOT/invalid/wrong-chain/user.schema.yaml.sig"
# expired
make_expired_ee "$ROOT/pki/signers/negatives/expired" "expired@example" "codeSigning" "$ROOT/pki/trust/companies/company-ca.pem" "$ROOT/pki/trust/companies/company-ca.key"
cms_sign "$ROOT/minimal/wasm/add.wasm" "$ROOT/invalid/expired/add.wasm.sig" \
  "$ROOT/pki/signers/negatives/expired/ee.pem" "$ROOT/pki/signers/negatives/expired/ee.key" "$ROOT/pki/trust/companies/company-ca.pem"
# oversize wasm
head -c 10485760 </dev/zero >"$ROOT/invalid/oversize/add_oversize.wasm" # 10 MiB file
# too-large-request payload
cat >"$ROOT/invalid/too-large-request/set.too-large.json" <<'JSON'
{"set": ["$(printf 'x%.0s' {1..200000})"]}
JSON

msg "All done. Tree: $ROOT"
