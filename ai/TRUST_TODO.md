# Relay Trust Chain — TODO

> Forrás: tead01.txt párbeszéd + CIC KB elemzés
> Dátum: 2026-03-28
> Cél: Háromrétegű, environment-bound relay trust modell megvalósítása

---

## Státusz jelölések

- `[implemented]` — kódban él, MCP-ben látható
- `[scaffold]` — infrastruktúra megvan, bekötés hiányzik
- `[concept]` — nincs runtime megfelelő

---

## Ami már megvan (ne tervezd újra)

| Komponens | Hol | Státusz |
|---|---|---|
| `VaultCryptoService` + `CryptoService` interface | `core/nexus/crypto/service.go` | implemented |
| `VerifyCertificateChain` + `VerifyOptions` (SPKI pin, CRL, trusted roots) | `core/cabinet/pki_verify.go` | implemented |
| `checkSPKIPinning` | `core/cabinet/pki_verify.go` | implemented |
| `BuildMeta` (build ID, commit hash, timestamp, GPG sig, CA fingerprint) | `tools/certutils/crt_parser.go` | implemented |
| `Cabinet` + `cabinetService` (schema/module/workflow store) | `core/cabinet/service.go` | implemented |
| `vault-sign-agent` (in-memory Vault, Transit ECDSA) | `CIC-Schemas/tools/vault-sign-agent.md` | implemented |
| `MANIFEST.sha256` CI drift check | `base/golang/.github/workflows/manifest-check.yml` | implemented |
| `main()` bootstrap (cabinet, WASM, PKI init, HTTP server) | `cmd/relay/main.go` | implemented |
| CA Hierarchy (Root CA, CICPlatformCA, CICDeveloperCA, CICSourceCA, CICLicenseCA) | `EEC/` (keygen_hierarchy.sh, Makefile, CRL) | implemented |
| `/set` generikus workflow executor endpoint | `cmd/relay/main.go` | implemented |

---

## Layer 0 — Schema Compilation API `[scaffold]`

**Cél**: a relay fordítási rétegének HTTP belépési pontja — a postgresql (és általában bármely schema) végigvitelének kapuja.

### 0.1 `POST /v1/schema/compile` endpoint

```go
// Request
type CompileRequest struct {
    Schema          string `json:"schema"`           // pl. "postgresql"
    Version         string `json:"version"`          // pl. "v0.18.0_2025"
    SourceAssertion string `json:"source_assertion"` // YAML, developer-signed
}

// Response
type CompileResponse struct {
    Artifact   string      `json:"artifact"`    // aláírt artifact YAML
    ProofTrace *ProofTrace `json:"proof_trace"` // teljes execution trace
}
```

**Feladatok:**
- [x] `POST /v1/schema/compile` handler: thin wrapper a `cabinet.Setx()` fölé
- [x] Request → `SetPayload` mapping (`workflow_id: "cic.schema.compile@1.0"`)
- [x] Response: artifact YAML + ProofTrace JSON
- [x] Routing bekötése `newMux()`-ba

### 0.2 `cic.schema.compile@1.0` workflow

**Feladatok:**
- [x] Workflow regisztrálása (`registerNativeComponents` → `svc.PutWorkflow`)
- [x] 3 modul regisztrálása native Go impl-lel
- [x] `AssertSource`: YAML parsing + name/version cross-check (stub cert validation)
- [x] `BuildSchema`: real artifact passthrough (vagy stub ha nincs source_assertion)
- [x] `SignArtifact`: real `build_hash = sha256(raw_artifact_yaml)`
- [x] End-to-end `/v1/schema/compile` → ProofTrace smoke test (stub + valódi source_assertion)

### 0.3 Validator véglegesítése a kimenet alapján

**Feladatok:**
- [ ] A valódi postgresql artifact kimenet alapján artifact séma rögzítése
- [ ] `cicSign` + `cicSignedCA` + `buildHash` mezők validátora
- [ ] `createdBy.certificate` chain → CICDeveloperCA ellenőrzés bekötése (`VerifyCertificateChain`)

---

## Layer 1 — Source Trust (fejlesztői eredet)

**Cél**: bizonyítható, hogy egy konkrét source tree-t egy jogosult fejlesztő hagyott jóvá.

### 1.1 Source Assertion manifest séma `[concept]`

```yaml
# relay_source_assertion.yaml
source_assertion:
  repo_id: string
  commit: string
  tree_hash: sha256:...       # teljes source tree hash
  intent_hash: sha256:...     # OIS intent binding
  developer_signature: base64
  developer_cert_chain:
    - developer.pem
    - company_intermediate.pem
  metadata:
    company_id: string
    developer_id: string
    timestamp: RFC3339
    policy_version: string
```

**Feladatok:**
- [ ] Séma definíció: `source_assertion.yaml` (Cabinet-kompatibilis)
- [ ] Validator: developer cert chain → CICDeveloperCA → Root CA
- [ ] Validator: `developer_signature` a konkrét `tree_hash + intent_hash` kombinációra vonatkozik (ne csak a személyre)
- [ ] Relay policy: csak `source_assertion`-nel ellátott input kerülhet feldolgozásra

---

## Layer 2 — Build Provenance (forrás → bináris kötés)

**Cél**: bizonyítható, hogy a futó image abból a hiteles source-ból épült.

### 2.1 Relay Release Assertion `[concept]`

```yaml
# relay_release_assertion.yaml
relay_release_assertion:
  source_tree_hash: sha256:...   # egyezés a source_assertion.tree_hash-sel
  build_recipe_hash: sha256:...
  toolchain_hash: sha256:...
  image_digest: sha256:...
  output_manifest_digest: sha256:...
  build_timestamp: RFC3339
  signature: base64
  issuer: CICSourceCA
  policy_version: string
```

**Feladatok:**
- [ ] `BuildMeta` kiterjesztése `ReleaseMeta`-ra: `source_tree_hash` és `build_recipe_hash` mezők hozzáadása
- [ ] CICSourceCA aláírás a teljes release hash-re (nem csak image digest)
- [ ] Bootstrap validator: `relay_release_assertion.source_tree_hash == source_assertion.tree_hash`
- [ ] Telepítő constraint: `image_digest` egyezés ellenőrzése indulás előtt

### 2.2 Signed IaC Bundle `[scaffold]`

A `MANIFEST.sha256` CI check megvan — bootstrap-szintű ellenőrzés hiányzik.

**Feladatok:**
- [ ] `main()` bootstrap: induláskor `MANIFEST.sha256` + CICSourceCA aláírás ellenőrzés
- [ ] Relay ne fogadjon el "latest" vagy külső fetch-et — csak a signed bundle-ből dolgozzon
- [ ] Runtime self-check: `hash(loaded_configs + binaries) == release_tree_hash`

---

## Layer 3 — Runtime Identity (példányazonosság)

**Cél**: a futó relay egyedileg azonosítható, identity-ja nem előre ismert és nem klónozható.

### 3.1 Runtime keypair generálás `[scaffold]`

A `VaultCryptoService` megvan — identity kulcs generálása és kezelése hiányzik.

**Feladatok:**
- [ ] Vault Transit: `identity` named key létrehozása induláskor (nem a repo-ból, nem a build-ből)
- [ ] `CryptoService` interface bővítése: `GenerateIdentityKey() (pubkey, error)`
- [ ] Pubkey export + tárolás (nem a privkey): relay identity pubkey a relay state-be kerüljön
- [ ] Kulcs soha ne kerüljön: git, build artifact, API seed, YAML config-ba

### 3.2 relayCA mint context attestor CA `[concept]`

A tead01.txt alapfelismerése: relayCA nem új identity-t oszt, hanem ugyanazt az identity-t köti új kontextushoz.

```
identity = public_key + vault-bound private key + context-bound PEM
```

PEM tartalma (relayCA adja ki):
- `release_hash`, `infra_hash`, `vault_context_hash`
- `environment_id = SHA256(release_hash + install_nonce + talos_cluster_id + vault_machine_pubkey)`
- `policy_version`, `expires_at`

**Feladatok:**
- [ ] `relayCA` felvétele az EEC CA hierarchiába (keygen_hierarchy.sh + cnf)
- [ ] relayCA context-bound PEM kiadási logika (nem statikus, runtime generált)
- [ ] PEM fingerprint explicit a signed release-ben (placeholder PEM build-time override helyett)

### 3.3 Environment Fingerprint `[concept]`

**Feladatok:**
- [ ] `EnvironmentCollector` komponens: gyűjti a futó környezet OID-jeit
  - Kubernetes cluster UID
  - Node UID / fingerprint
  - Namespace UID
  - Cert serial-ok (Talos, relay cert)
  - Vault instance ID
- [ ] `env_fingerprint = sha256(sorted(OIDs))`

### 3.4 Negáló HMAC checksum `[concept]`

**Feladatok:**
- [ ] Vault Transit: dedikált HMAC kulcs az environment bindinghoz
- [ ] `neg_checksum = HMAC(vault_hmac_key, bitwise_NOT(env_OIDs) || bitwise_NOT(timestamp_bytes))`
- [ ] Két-csatornás verifikáció: OID direkt ellenőrzés ÉS negáló checksum egyezés
- [ ] Checksum belekerül minden aláírt payloadba

---

## Layer 4 — Challenge-Response Protocol `[concept]`

**Cél**: az identity kulcs birtoka bizonyítható, replay nem lehetséges.

### 4.1 Challenge endpoint

**Feladatok:**
- [ ] HTTP endpoint: `POST /v1/relay/challenge` → nonce kibocsátás (rövid TTL, egyszer használható)
- [ ] Challenge response payload:

```go
type ChallengeResponse struct {
    Challenge      []byte    // klienstől kapott nonce
    ReleaseHash    string    // release_assertion.image_digest
    EnvFingerprint string    // sha256(OIDs)
    NegChecksum    []byte    // HMAC(vault_key, ~OIDs || ~timestamp)
    Timestamp      time.Time
    InstanceNonce  []byte    // per-session random
    Signature      []byte    // sign(all above, identity_private_key via Vault)
}
```

- [ ] `VaultCryptoService.Sign` bekötése a challenge response-ba (megvan az interface)
- [ ] Kliens oldali verifikátor: pubkey pin + signature + release hash + timestamp ellenőrzés

---

## Layer 5 — Bootstrap Token Flow `[concept]`

**Cél**: csak valid telepítésből induló relay kapjon Vault hozzáférést.

**Feladatok:**
- [ ] Token kibocsátó: rövid TTL (30s–2m), egyszer használható, context-bound
- [ ] Token payload: `release_hash + install_nonce + timestamp + (opcionális: cluster_id)`
- [ ] Vault policy: token validálás → `release_hash` egyezik ismert release-szel → engedélyez
- [ ] Vault: csak tokenes kérésnél generál identity kulcsot vagy ad ki credentialt
- [ ] Token kiadás feltételei: rate limit, egyszer használható (`response_wrapping`), időablak

---

## Layer 6 — CA Hierarchy `[scaffold]`

**Cél**: tiszta, szétválasztott CA struktúra a négy trust domain-hez.

```
CIC Root CA (rootCA2026.pem) — self-signed, pathlen:3
├── CICPlatformCA   — pathlen:2                         [implemented]
├── CICDeveloperCA  — pathlen:0 → developer leaf certek [implemented]
├── CICSourceCA     — pathlen:0 → source + release sig  [implemented]
├── CICLicenseCA    — pathlen:1                         [implemented]
└── relayCA         — context attestor CA               [concept]
    └── context-bound PEM (runtime, nem statikus)
```

**Feladatok:**
- [ ] `relayCA` felvétele a hierarchiába (EEC/relayCA.cnf + keygen_hierarchy.sh)
- [ ] Company intermediate kiadási folyamat (cég kérvényezi CICDeveloperCA-tól)
- [ ] `VerifyOptions`-ba: mindhárom CA trust bundle bekötése kontextusonként szétválasztva
- [ ] Relay policy: source validation trust bundle ≠ relay runtime trust bundle (ne mosd össze)

---

## Layer 7 — Audit és Monitoring `[scaffold]`

A Cabinet + relay pipeline logol, de trust-specifikus audit nincs.

**Feladatok:**
- [ ] Strukturált audit log minden trust döntésnél:
  - source assertion validáció eredménye
  - release hash egyezés
  - challenge-response eredménye
  - bootstrap token felhasználás
- [ ] Drift detection: `runtime_hash != release_tree_hash` → alert + opcionálisan kill
- [ ] Instance registry: trusted pubkey-k nyilvántartása (külső entitás oldal)

---

## Megvalósítási sorrend (frissített)

```
1. Schema Compilation API (L0)       → első valódi end-to-end flow
2. Source Assertion séma (L1)        → developer flow, L0.1 előfeltétele
3. Release Assertion + BuildMeta (L2) → CI/build pipeline
4. relayCA context attestor (L6+L3.2) → CA hierarchia lezárása
5. Runtime Identity keygen (L3.1)    → Vault bekötés
6. Bootstrap Token (L5)              → Vault policy
7. Challenge-Response (L4)           → relay endpoint
8. Env Fingerprint + neg checksum (L3.3–3.4) → environment binding
9. Audit (L7)                        → végig párhuzamosan
```

---

## Ami szándékosan kimaradt

- **SEV/TDX**: nem kontrollált hardver — Talos + Vault mlock védelme elegendő a szoftveres maximumhoz
- **TPM**: következő iteráció, ha a provider támogatja
- **100%-os hamisíthatatlanság**: információelméleti korlát, nem cél — a cél a trust megszerzésének kontrollálása