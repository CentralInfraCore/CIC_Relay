# Relay Roadmap (AI snapshot)

Ez a fájl az AI-támogatott roadmap aktív állapota.
→ Lezárt milestone részletek + régi sprintek: [ROADMAP_archive.md](ROADMAP_archive.md)
→ Részletes fejlesztői doksi: [docs/hu/roadmap/relay_milestones.md](../docs/hu/roadmap/relay_milestones.md)

---

## Jelenlegi állapot (STATE)

| Milestone | State       | Megjegyzés |
|-----------|-------------|------------|
| M-Konfig  | **Done**    | Séma-vezérelt önvalidáció kész (M8.1 + M8.2) |
| M0        | Done        | Tesztek refaktorálása — dinamikus checksum |
| M1        | Done        | WASM kanonikus checksum, CAS tárolás |
| M2        | Done        | ExecutionGraph, schema validáció, ProofTrace |
| M3        | Done        | PKI verify + bootstrap.go wiring + 3 PKI teszt |
| M4        | Done        | LIMITS: JSON/WASM méret + CPU timeout + schema config |
| M5        | Done        | Uniform JSON error, sentinel namespacing, HTTP status map |
| M6        | Done        | Strukturált log, audit trail, /healthz /readyz, E2E smoke |
| M7        | Done        | symbols drift CI, vet/staticcheck, per-pkg coverage, HTML report |
| M8        | Done        | CIC_ env prefix, dogfooding config, secrets separation |
| M9        | Done        | Rate limiter, concurrent cap, slow-loris; statikus release + SBOM |
| M10       | Done        | M9-be olvadt |
| M11       | Done        | IaCSource, FileSource, UpstreamSource, Loader, Graph |
| M12       | Done        | GitStateRecorder, WorkflowRecorder, Setx összekötés |
| M13       | Done        | Vault Transit signing, dev-vault bootstrap, prod recorder wiring |
| M14       | Done        | Audit sync: squash + push + prune, integrációs teszt 83.3% |
| M15       | Done        | SubprocessWorker IPC, RunWorkerLoop, --mode=module-worker |
| M16       | Done        | PollingWatcher, BootstrapToken, Onboard/Offboard, main.go wiring |
| M17       | Done        | ProofTrace.SourceDigest + computeChainHash anchoring |
| M18       | Done        | PollingWatcher scanDir + emitDiff (CREATE/UPDATE/DELETE) |
| M19       | Done        | ci.build@1.0 native Go modul + schema |
| M20       | Done        | pkg/sourcedigest tree hash + tools/sourcehash CLI + ldflags |
| M21       | Done        | Docker image digest-pin + CI Go 1.24.6 exact + staticcheck@v0.6.1 |
| M22       | Done        | pkg/obs alaptípusok, hash-lánc, sink interfészek, MetricInstrument |
| M22.6     | Done        | Obs relay wiring: setHandler TraceFrame + CounterPoint + LogRecord |
| M22.7     | Done        | LogrusSink: obs → logrus JSON bridge |
| M22.8     | **Done**    | cicwasm obs wiring: compile/instantiate metrikák + dealloc WARN logok |
| M23       | Done        | ResourceSpec + IaCValidator + Service operator handling |
| M24       | Done        | CabinetActivator + dependency gating + state.desired → Setx |
| M25       | Done        | PKI Trust Bootstrap: embedded dev CA + CIC_TRUST_MODE |
| M26       | Done        | L0.3 Source Assertion: CertVerifyFunc + X.509 chain verify |
| M27       | Done        | L0.4 Spec Checksum: metadata.checksum = sha256(canonical_json(spec)) |
| M28       | **Done**      | Git Host Functions: wazero git primitívek WASM moduloknak |
| M29       | **Done**      | GitSource: IaC konfig betöltés git repóból |
| M30       | **Done**      | GitRegistry auto-wiring: CIC_INFRA_REPO_PATH + CIC_GIT_SOURCE_LOCAL_PATH auto-regisztráció |
| M31       | **Done**      | obs wiring for git: relay.git.op_duration_ms histogram + WARN logok ExecGitOps-ban |
| M32       | **Done**      | TraceFrame chain_anchor: setHandler link TraceFrame → ProofTrace.ChainHash |
| M33       | **Done**      | pkg/merkle: Merkle-root alapú build provenance hash (VerificationManifest, BuildVerificationRoot) |
| M34       | **Done**      | Merkle integrációk: BuildContext ldflags, NewSignArtifactFunc → verification_root, bootstrap wiring |
| M35       | **Done**      | BuildContext feltöltése: GoRuntimeDigest, BuilderImageDigest ldflags, CAFingerprint PKIAssets-ből |
| M36       | **Done**      | Layer 2 CICSourceCA signing: PKIAssets.CertChainHash, bctx wiring, cic_signed_ca = CAFingerprint |
| M7.5–7.7  | **Done**      | CI quality gate: per-package coverage küszöbök, govulncheck (advisory), mutation job |

---

## Scaffold / Bridge térkép

| Komponens | Státusz | Előfeltétel |
|---|---|---|
| `UpstreamSource` (HTTP IaC source) | scaffold | relay federáció v2 |
| `LocalWorker` in-process | scaffold | 3-process isolation + Vault |
| `cicSign` / `cicSignedCA` mezők | **implemented** | M36 ✅ — CAFingerprint-alapú |
| quorum döntési réteg | concept | nincs runtime megfelelő |
| CICmeta protocol | concept | nincs élő bridge a Relay runtime-hoz |

---

## Sprint 12 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p46  | M29       | Done    | GitSource: IaC konfig betöltés git repóból (relay infrastruktúra) |
| p47  | M28       | Done    | Git Host Functions: wazero primitívek WASM moduloknak |

---

## Sprint 13 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p48  | M30       | Done    | GitRegistry auto-wiring: env-alapú repo regisztráció induláskor |
| p49  | M31       | Done    | obs ExecGitOps: relay.git.op_duration_ms + WARN logok |
| p50  | M32       | Done    | TraceFrame chain_anchor: setHandler → ProofTrace.ChainHash link |

---

## Sprint 14 — Quality Enforcement — Lezárva

### Kontextus

A sprint 13 végén lezajlott egy átfogó teszt-minőségi sprint (2026-05-16):
- Coverage: 80.4% → **89.6%** összesített; 15 csomag 90% fölé emelve
- Gremlins mutációs tesztek bevezetve: cabinet 95.9%, schemapipeline 95.4%, schemacompile 96.4%
- `make mutate` / `make mutate-all` targetek + 90%-os efficacy threshold
- Per-package `coverage-check-pkgs` küszöbök frissítve
- Stop hook: automatikus teszt futtatás a változtatott csomagokon (asyncRewake)

### Hiányok — amit CI nem kényszerít ki

| Gap | Ahol kellene | Állapot |
|-----|-------------|---------|
| `mutate-all` nincs CI-ban | `verify.yml` | ❌ Lokális only |
| `govulncheck` nincs CI-ban | `verify.yml` | ❌ Hiányzik |
| CI coverage küszöbök elavultak | `verify.yml` L83–89 | ❌ Régi értékek |

### Sprint 14 feladatok

| Task | Milestone | Prioritás | Leírás |
|------|-----------|-----------|--------|
| p51  | M7.5      | ✅ Done   | CI coverage küszöbök szinkronizálása (`verify.yml` → Makefile értékek) |
| p52  | M7.6      | ✅ Done   | `govulncheck` hozzáadása CI-hoz (advisory, Go 1.25+ után hard gate) |
| p53  | M7.7      | ✅ Done   | Mutation CI gate: cabinet + schemapipeline + schemacompile, 90% threshold |
| p54  | —         | 🟢 Todo   | `cmd/relay/main.go` 80%→90%: injectable hook `buildOperatorIfConfigured` + `buildRecorderOption` |

---

### M36 — ✅ Done

**Layer 2: CICSourceCA signing wiring**
- `PKIAssets.CertChainHash`: összes CA cert DER sha256-a; `loadPKIAssets` tölti ki
- `bctx.CAFingerprint` + `bctx.CertChainHash`: közvetlenül `pkiAssets`-ből töltve a `run()`-ban
- `cic_signed_ca`: `bctx.CAFingerprint` ha PKI betöltve; `"stub:pending"` ha placeholder
- 3 új teszt: stub when no PKI, fingerprint when loaded, verification_root sensitivity
- Mutáció: schemacompile 96.30% → **96.55%** ✅

---

## Sprint 15 — aktív

| Task | Milestone | Prioritás | Leírás |
|------|-----------|-----------|--------|
| p54  | —         | ✅ Done   | `cmd/relay/main.go` 80%→**90%**: injectable seams + 40+ célzott teszt |
| p55  | —         | ✅ Done   | Go 1.24.6 → 1.25.10 toolchain upgrade; govulncheck hard gate feloldása CI-ban |
| p56  | M37       | 🟢 1      | `UpstreamSource` HTTP implementáció (relay federáció v2 előkészítés) |
| p57  | —         | ✅ Done   | fix(B1): `ComputeChainHashV1` export — verifier és teszt egységes forrást hív |
| p58  | —         | ✅ Done   | fix(B2): manifest timestamp eltávolítva, `make manifest-update` unified |
| p59  | —         | ✅ Done   | fix(B3): PKI prod fail-closed — `CIC_PLATFORM_CA_BUNDLE` kötelező, hiány = error |

---

## Azonosított hibák — külső review (2026-05-17)

Forrás: külső technikai review a `cic-relay-20260517.tar.gz` alapján.
Minden hiba verifikálva a tényleges kóddal szemben.

### B1 — ProofTrace hash algoritmus duplikáció (P0)

**Fájlok:** `core/cabinet/proof_trace.go:59`, `cmd/relay/proof_verify.go:166`, `cmd/relay/proof_verify_test.go:14`

**Probléma:** A `computeChainHash` (cabinet) a hash-t `"CICCHAINv1"\x00` domain prefixszel kezdi.
A `recomputeChainHash` (verifier) ezt a prefixet kihagyja — ezért minden valódi `ProofTrace`
artifact verifikációja `chain_hash mismatch` hibával bukna el.

A teszt helper (`chainHashFor`) szintén a hibás algoritmust másolja, ezért CI zöld marad
— a hiba csak cross-package integrációs szinten látható.

**Státusz:** ✅ **Done** (2026-05-17) — `ComputeChainHashV1` exportálva, verifier és tesztek egységes forrást hívnak.
Task: `p57`

---

### B2 — Manifest timestamp: CI drift-check mindig bukik (P1)

**Fájlok:** `scripts/gen_manifest.sh:16`, `.github/workflows/manifest-check.yml:22`

**Probléma:** `gen_manifest.sh` a manifest fejlécébe beleírja az aktuális UTC időbélyeget:
```bash
echo "# generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
```
A CI `diff -u MANIFEST.sha256 /tmp/MANIFEST.sha256`-ot futtat — a timestamp miatt
a diff mindig eltér a commitolt fájltól. A manifest-check CI gate funkcionálisan törött.

Külön probléma: `make manifest-update` (`git ls-files -z | xargs -0 sha256sum`) más scope-ot
és más formátumot gyárt mint `gen_manifest.sh` (`find ai context GOLDEN`) — a két parancs
inkompatibilis, a Makefile-lal frissített MANIFEST.sha256 a CI-on nem megy át.

**Státusz:** ✅ **Done** (2026-05-17) — timestamp eltávolítva, `make manifest-update` a `gen_manifest.sh`-t hívja, `MANIFEST.sha256` újragenerálva.
Task: `p58`

---

### B3 — PKI prod mode fail-open (P1/P2)

**Fájl:** `cmd/relay/pki_bootstrap.go:52–64`

**Probléma:** `CIC_TRUST_MODE=prod` esetén `bootstrapPKI` üres cert poolt ad vissza
és csak WARN-t logol — nem tér vissza hibával. Egy prod relay CA bundle nélkül is elindul,
és a PKI ellenőrzés csendesen ki van kapcsolva.

```go
case TrustModeProd:
    logger.Warn("PKI verification skipped in prod mode (scaffold)")
    return &PKIAssets{Roots: x509.NewCertPool(), ...}, nil  // fail-open
```

Ez dokumentált scaffold — de a jelenlegi viselkedés nem jelzi vissza a hívónak,
hogy a trust store üres. Prod deployban ez észrevétlenül kikapcsolt PKI ellenőrzést jelent.

**Státusz:** ✅ **Done** (2026-05-17) — `CIC_PLATFORM_CA_BUNDLE` kötelező prod módban; hiány esetén error, 3 teszt fedezi.
Task: `p59`

---

### Nem blokkolt, külön sprint

| # | Probléma | Megjegyzés |
|---|----------|-----------|
| B4 | HTTP API: nincs auth/mTLS boundary | Architektúra döntés, külön sprint |
| B5 | `ipRateLimiter` map nem takarít (memória) | Nem internet-facing, alacsony prioritás |
| B6 | Git host functions: nincs műveletszintű capability policy | M37+ scope |

---

## Tervezett milestone-ok (részletes)

### M30–M31 — Done (archívum)
Részletek: `ROADMAP_archive.md`

---

## Capability Runtime Roadmap (M38–M49)

### Kontextus és alapelv

A CIC-Relay fejlődjön tovább determinisztikus canonical/cabinet motorból egy kontrollált,
capability-secured relay runtime irányba, ahol:

```text
WASM modul: provider-aware, de nem secret-aware
Relay:       secret/capability-aware, de nem provider-specific
```

**A v1 contract nem sérül.** A capability runtime külön execution profile, nem a canonical
path módosítása. Az M0–M36 által lefektetett determinisztikus alap érintetlen marad.

**Döntési szabály (végig érvényes):**

```text
Relay-ben nincs OCI SDK.
WASM-ban nincs Vault API.
WASM-ban nincs provider signing private key (OCI/signing kulcs Vault-ban marad).
Relay generic capability kernelt ad.
Vault = key custody / signing backend.
Provider logika WASM-ban marad.
Relay nem látja a secret plaintextjét — csak a modul tudja dekriptálni (envelope encryption).
```

**B6 feloldás:** ez a roadmap oldja fel a `B6` azonosítójú nyitott hibát
("Git host functions: nincs műveletszintű capability policy — M37+ scope").

**M37 / capability M5 szétválasztás:**
- M37 (`UpstreamSource`) = Relay tölt le IaC-ot upstream Relay-ből → Relay–Relay kommunikáció
- M43 (`http.request` broker) = WASM modul kér HTTP-t a host-tól → WASM–külvilág
- A kettő párhuzamos fejlesztési vonal, nem egymás előfeltétele.

---

### 10 döntési kérdés — spec előtt kötelező

Ezek megválaszolása nélkül a capability spec nem írható meg:

```text
1.  Maradjon-e a canonical profile teljesen side-effect free?
2.  A capability runtime külön profile legyen-e?
3.  Első provider tényleg OCI legyen?
4.  Első crypto backend Vault Transit legyen?
5.  Első HTTP capability csak allowlistelt HTTPS hostokra menjen?
6.  Kell-e tenant/multi-tenant már az első verzióban?
7.  OCI-ból első demo: listCompartments vagy listInstances?
8.  A WASM modul írhat-e bármilyen logot, vagy csak trace.emit-en át?
9.  Engedünk-e secret.envelope_read capability-t, vagy első körben csak crypto.sign?
10. Production cél, vagy először PoC/reference runtime?
11. (architekturális invariáns, nem nyitott kérdés — lásd lent)
```

**11. pont — kétkulcsos envelope encryption (sarokpont, nem opció):**
Ha a WASM modulnak plaintext secretre van szüksége, azt kizárólag ő dekriptálhatja —
a Relay soha nem látja a plaintextet (envelope encryption: `secret.envelope_read`
+ modul key pair). Ez architekturális invariáns, nem Q&A.
Következménye: M39 manifest schema tartalmazza a `recipientPublicKey` mezőt,
M41.2 (Spec E2) az M41 logikai párja. OCI signing flow ezen **nem megy át** —
ott `crypto.sign` az út, `secret.envelope_read` csak valódi plaintext credential esetén.

**9. kérdés kockázatjelölés:** a `relay.secret.envelope_read` capability — ha valaha
megvalósul — közvetlenül érinti a döntési szabályt ("WASM-ban nincs secret"). Ez a
capability külön policy design szprintet igényel; addig a manifest schema-ból ki kell
hagyni (ne lehessen deklarálni, amit a Relay még nem tud megtagadni). Az első
verzióban csak `crypto.sign` engedélyezett, `secret.envelope_read` **concept**, nem scaffold.

---

### Minimális spec-csomag — issue bontás előfeltétele

Nem egy monolit specifikáció kell, hanem 8 rövid döntési spec. Ezekből lesz
majd rendes issue-k, PR-sorrend és DoD.

| Spec | Cím | Célmilestone |
|------|-----|-------------|
| A | Execution Profile Spec | M38 |
| B | Capability Manifest Schema | M39 |
| C | Hostcall ABI | M40 |
| D | `crypto.sign` Spec | M41 |
| E | Key Custody / Vault Adapter Spec | M41 |
| E2 | WASM modul key pair + `secret.envelope_read` envelope encryption | M41.2 |
| F | `http.request` Broker Spec | M43 |
| G | Error + Audit Spec | M44 + M45 |
| H | OCI Reference Provider Spec | M46 |

---

### Milestone táblázat (M38–M49)

| Milestone | Cím | Státusz | Megjegyzés |
|-----------|-----|---------|-----------|
| M38 | Execution profile split | concept | Spec A szükséges |
| M39 | Capability manifest schema | concept | Spec B szükséges |
| M40 | Generic hostcall ABI | concept | Spec C; M28 git host functions mintájára |
| M41 | `crypto.sign` capability | concept | Spec D+E; Vault Transit (M13 minta) |
| M41.2 | WASM modul key pair + envelope encryption (`secret.envelope_read`) | concept | Spec E2; modul saját kulcspár, Relay csak ciphertextet lát |
| M42 | Egységes error model | concept | Spec G (hiba rész) |
| M43 | `http.request` broker | concept | Spec F; M37-től független vonal |
| M44 | Limits / budget | concept | Spec G (limit rész) |
| M45 | Audit / ProofTrace integráció | concept | Spec G (audit rész); M17/M22/M32 bővítés |
| M46 | OCI reference provider | concept | Spec H; `examples/providers/oci/` |
| M47 | Policy test matrix | concept | Spec-ek negatív tesztjei |
| M48 | CI / release gate | concept | capability schema drift + golden drift |
| M49 | Production hardening | concept | mTLS, per-tenant policy, circuit breaker |

---

### Milestone részletek

#### M38 — Execution profile split

**Cél:** a determinisztikus canonical path és a capability path ne keveredjen.

```yaml
execution:
  profile: canonical | capability
```

**Canonical profile (v1 contract, változatlan):**
```text
nincs http / crypto.sign / clock / random / secret / külső I/O
minden hostcall: ErrCapabilityDenied
```

**Capability profile:**
```text
csak manifestben deklarált capability-k
csak policy által engedélyezett hostok/kulcsok
minden művelet auditált és limitált
```

Döntések:
- Alapértelmezett profil: `canonical`
- Capability profile csak explicit manifest jelenlétével
- Runtime profilváltás: **nem engedett**
- `ExecutionProfile` / manifest viszony: manifest hiánya = canonical; manifest jelenléte = capability

**Implementációs megjegyzés:** a meglévő `MockCicWasmHost` / `MockWasmManager` (SYMBOLS.md)
az analóg minta az `ExecutionProfile` tesztelhetőségéhez.

**DoD:**
- Canonical golden nem változik (`make verify` zöld)
- Capability nélküli modul nem tud hostcallt hívni
- Hibakód: `ErrCapabilityDenied`

---

#### M39 — Capability manifest schema

**Cél:** a WASM modul ne Vault-policy-t kapjon, hanem Relay capability-policy-t.

```yaml
name: oci-provider
version: 0.1.0

execution:
  profile: capability

capabilities:
  http:
    allow:
      - host: iaas.eu-frankfurt-1.oraclecloud.com
        methods: ["GET", "POST", "PUT", "DELETE"]
  crypto:
    sign:
      - keyHandle: oci-prod-api-key
        algorithms: ["rsa-sha256"]
  clock:
    allow: true

limits:
  maxHttpRequests: 100
  maxRequestBodyBytes: 1048576
  maxResponseBodyBytes: 1048576
  timeoutSeconds: 30
  maxSignRequests: 50
  maxSignPayloadBytes: 32768

logging:
  logHttpBodies: false
  redactHeaders:
    - authorization
    - x-vault-token
```

**Tiltott a manifest schema-ban (első verzió):**
- `relay.kv.*` — concept
- wildcard host (`*`) — alapból tiltott

**`secret.envelope_read` a manifestben (M41.2 után engedett):**
```yaml
capabilities:
  secret:
    envelope_read:
      - secretHandle: oci-api-key-config
        recipientPublicKey: base64:...   # modul key pair public kulcsa
```

**DoD:**
- Hibás manifest fail-fast
- Unknown capability tiltott
- Unknown keyHandle tiltott
- Wildcard host tiltott

---

#### M40 — Generic hostcall ABI

**Cél:** egységes, provider-agnosztikus hostcall modell (M28 git host functions mintájára).

```text
hostcall(req_ptr i32, req_len i32, out_ptr i32, out_len i32) -> i32
```

Envelope:
```json
{
  "capability": "relay.crypto.sign",
  "traceId": "...",
  "input": {}
}
```

Válasz:
```json
{
  "status": 0,
  "traceId": "...",
  "output": {}
}
```

**Capability-k v1:**
```text
relay.http.request
relay.crypto.sign
relay.clock.now
relay.trace.emit
```

**Tervezett (M41.2 előfeltétellel, de nem v1):**
```text
relay.secret.envelope_read(secretHandle, recipientPublicKey) -> ciphertext
  ← csak akkor, ha a modulnak tényleg plaintext credential kell
  ← modul key pair szükséges (M41.2)
  ← Relay nem látja a plaintextet, csak titkosítva adja át
relay.random.bytes
```

**Concept (külön sprint):**
```text
relay.kv.get / relay.kv.put
```

**DoD:**
- Minden hostcall ugyanazt az envelope formátumot használja
- Minden válasz tartalmaz `status`, `error`, `trace_id`
- ABI fuzz / boundary tesztek

---

#### M41 — `crypto.sign` capability

**Cél:** private key soha ne kerüljön WASM modulba.

```text
WASM felelőssége:
  - provider request canonicalization
  - signing string előállítás
  - Authorization header összeállítás

Relay felelőssége:
  - keyHandle ellenőrzés
  - Vault Transit adapter (M13 minta)
  - aláírás végrehajtása
  - audit
```

Hostcall input:
```json
{ "keyHandle": "oci-prod-api-key", "algorithm": "rsa-sha256", "payload": "base64..." }
```

Válasz:
```json
{ "signature": "base64...", "keyVersion": "v3", "traceId": "..." }
```

**Vault adapter döntések:**
- keyHandle → Vault key mapping: Relay konfigurációban
- Ki definiálhat keyHandle-t: csak Relay operator
- Vault hiba: `ErrCapabilityBackendUnavailable`, **nincs silent allow**
- Signature cache: első körben nem

Audit események: `crypto.sign.requested`, `crypto.sign.allowed`, `crypto.sign.denied`

**DoD:**
- `secret.read` nincs
- Csak engedélyezett keyHandle + algorithm
- Aláírás auditálva payload hash-sel (nem nyers payload dump)

---

#### M41.2 — WASM modul key pair + `secret.envelope_read` envelope encryption

**Cél:** ha a WASM modulnak tényleg plaintext credentialra van szüksége, azt **csak
ő tudja dekriptálni** — a Relay soha nem látja a plaintextet. Ez az architektúra
kétkulcsos titkosítási sarokpontja.

**Mechanizmus (envelope encryption):**

```text
1. WASM modulnak saját kulcspárja van (privát kulcs WASM-ban marad)
2. A modul public key-je a manifest-ben van regisztrálva
3. Hostcall:
   secret.envelope_read(secretHandle, recipientPublicKey) -> ciphertext
4. Relay lekéri a secretet Vault-ból, AZONNAL enkriptálja a modul public key-jével
5. Relay visszaadja a ciphertextet — plaintext soha nem kerül Relay memóriájába
6. Csak a modul tudja dekriptálni a saját private key-jével
```

**Flow:**
```text
WASM → hostcall(secretHandle, myPublicKey) → Relay
                                               ↓ Vault: secret lekérés
                                               ↓ encrypt(secret, recipientPublicKey)
                                             ciphertext ← Relay ← Vault
WASM ← ciphertext
WASM: decrypt(ciphertext, myPrivateKey) → plaintext
```

**Fontos különbség:** ez nem ugyanaz a private key, ami az OCI signing-hoz kell.
- OCI signing private key: **Vault-ban marad**, WASM soha nem kapja meg
- Modul envelope private key: **WASM-ban van**, csak saját secretjeinek dekriptálásához

**Mikor kell:** csak akkor, ha a modul tényleg plaintext credentialra szorul
(pl. OCI instance principal config, amely nem aláírandó payload, hanem runtime adat).
Az OCI API signing flow `crypto.sign`-on megy — ott `secret.envelope_read` nem kell.

**Implementációs fájlok:**
```text
WASM-ban:   secret_envelope_client.go  — envelope decrypt, key pair kezelés
Relay-ban:  cmd/relay/secret_host_funcs.go  — hostcall handler, Vault wrapping
```

**Manifest:**
```yaml
capabilities:
  secret:
    envelope_read:
      - secretHandle: oci-api-key-config
        recipientPublicKey: base64:...   # modul key pair public kulcsa
```

**Scaffold állapot:** concept — implementálható M41 (Vault Transit adapter) után,
nem blokkol más capability-t.

**DoD:**
- Relay nem logol plaintext secretet
- Relay memóriájában csak ciphertext él
- secretHandle nem egyező → `ErrCapabilityKeyDenied`
- recipientPublicKey formátum validált
- Mock Vault-tal unit tesztelhető (`secret_envelope_client.go` + test transport)
- Audit event: `secret.envelope_read.allowed` / `secret.envelope_read.denied` — plaintext nélkül

---

#### M42 — Egységes error model

```text
ErrCapabilityDenied
ErrCapabilityLimitExceeded
ErrCapabilityTimeout
ErrCapabilityPolicyInvalid
ErrCapabilityBackendUnavailable
ErrCapabilityRedactionViolation
ErrCapabilityHostDenied
ErrCapabilityKeyDenied
ErrCapabilityAlgorithmDenied
```

JSON forma:
```json
{
  "error": {
    "code": "CAPABILITY_DENIED",
    "message": "http host is not allowed",
    "capability": "relay.http.request",
    "traceId": "..."
  }
}
```

**DoD:**
- Nincs nyers `http.Error` capability útvonalon
- Minden hiba gépileg olvasható
- HTTP mapping dokumentált
- Golden error tesztek

---

#### M43 — `http.request` broker

**Cél:** WASM modul tudjon HTTP requestet építeni, de a valódi hálózati kijáratot
a Relay kontrollálja. **Független az M37 (UpstreamSource) vonaltól.**

**Relay felelőssége:**
```text
egress allowlist, method allowlist, timeout, body/header limit,
redaction, trace, request count, response size limit
```

**WASM felelőssége:**
```text
provider endpoint mapping, request body, provider-specific headers,
response parsing, retry policy (ha explicit engedett)
```

**Egress policy döntések:**
- Redirect: alapból tiltott
- Wildcard host: alapból tiltott
- Request/response body log: alapból false
- Authorization header: mindig redact

**DoD:**
- Nem engedélyezett host/method: deny
- Response body limit működik
- Authorization header nem logolódik
- Request/response trace keletkezik

---

#### M44 — Limits / budget

```yaml
limits:
  maxHttpRequests: 100
  maxSignRequests: 50
  maxRequestBodyBytes: 1048576
  maxResponseBodyBytes: 1048576
  maxSignPayloadBytes: 32768
  timeoutSeconds: 30
  maxExecutionMillis: 30000
```

**Limit scope:** első verzióban per-invocation; per-tenant M49-re halasztva.

**DoD:**
- Limit túllépés determinisztikus hibával áll meg
- Timeout kontextusból öröklődik
- Nincs végtelen HTTP loop

---

#### M45 — Audit / ProofTrace integráció

Bővíti az M17 (ProofTrace.SourceDigest) és M22 (obs) alapokat.

**Logolható:**
```text
module_id, capability, host, method, keyHandle, algorithm,
payload_sha256, body_sha256, status, duration_ms, trace_id, decision: allow/deny
```

**Soha nem logolható:**
```text
private key, raw secret, Authorization header, Vault token,
teljes request body (default), teljes response body (default)
```

Redaction: default-on. Audit eventek strukturáltak. Trace ID átmegy minden hostcallon.

---

#### M46 — OCI reference provider

**Cél:** referencia WASM modul, amely bizonyítja az architektúrát.

```text
examples/providers/oci/
  module.yaml
  main.go
  relay_http_transport.go
  oci_signing_string.go
  oci_authorization.go
  compute_instances.go
```

**WASM-ban van:** OCI endpoint mapping, signing string canonicalization,
Authorization header construction, response normalization.

**WASM-ban nincs:** Vault API kliens, private key, OCI credential plaintext, direct socket/TLS.

Első demo: `listInstances` vagy `listCompartments` — döntés a Spec H-ban.

**DoD:**
- OCI signing string golden teszt
- `crypto.sign` mockkal unit tesztelhető
- `http.request` mockkal unit tesztelhető
- Valódi OCI/Vault integráció csak külön e2e flaggel

---

#### M47 — Policy test matrix

**Kötelező negatív tesztek:**
```text
canonical profile alatt hostcall → deny
unknown host → deny
unknown method → deny
unknown keyHandle → deny
wrong algorithm → deny
too many HTTP requests → deny
too large response → deny
Authorization header logolási kísérlet → redact
Vault unavailable → ErrCapabilityBackendUnavailable
```

**Kötelező pozitív tesztek:**
```text
engedélyezett crypto.sign működik mock signerrel
engedélyezett http.request működik mock transporttal
OCI signing string golden egyezik
redaction működik
audit event keletkezik
```

**DoD:** minden deny auditált, gépi hibakódot ad, nincs panic, nincs secret leak.

---

#### M48 — CI / release gate

```text
make test-capabilities
make test-policy
make test-redaction
make test-race
```

Artifact gate: SBOM, binary hash, source tree hash, signed manifest,
capability schema hash.

**DoD:** capability schema drift fail, golden drift fail, redaction teszt fail = build fail.

---

#### M49 — Production hardening

mTLS / internal service auth, per-module concurrency limit, global rate limit,
per-tenant capability policy, Vault outage behavior, retry/backoff, circuit breaker,
metrics dashboard.

**Vault kiesés soha nem okoz silent allow. Policy default: deny.**

---

### Javasolt verziósorrend

```text
v1.0  = canonical only (jelenlegi állapot)
v1.1  = capability schema + disabled runtime (M38–M39)
v1.2  = crypto.sign mock + tesztek (M40–M41)
v1.3  = http.request broker mock + tesztek (M43)
v1.4  = Vault Transit adapter + audit (M41 éles + M45)
v1.5  = OCI reference provider (M46)
v2.0  = production capability runtime (M49)
```

---

## Tervezett milestone-ok (részletes)

### M30–M31 — Done (archívum)
Részletek: `ROADMAP_archive.md`

---

## Sync-megjegyzés

Ezt a fájlt az AI-támogatott beszélgetések frissítik.
A hivatalos, részletes forrás: `docs/hu/roadmap/relay_milestones.md`