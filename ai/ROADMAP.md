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
Relay:       key-custody-aware és capability-policy-aware,
             de nem provider-specific és nem plaintext-secret broker
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

Alapértelmezett modellben sem a WASM, sem a Relay nem kezel provider signing private key-t.
Signing művelethez crypto.sign használandó.

Plaintext secret átadás nem része a v1 capability runtime-nak.
Ha később szükséges secret.envelope_read, annak külön design spike kell:
  A) Vault/KMS végzi a recipientPublicKey alapú wrappinget → Relay nem lát plaintextet
  B) Relay trusted brokerként transient plaintextet kezel → gyengébb, de egyszerűbb
```

**B6 feloldás:** ez a roadmap oldja fel a `B6` azonosítójú nyitott hibát
("Git host functions: nincs műveletszintű capability policy — M37+ scope").

**M37 / capability M5 szétválasztás:**
- M37 (`UpstreamSource`) = Relay tölt le IaC-ot upstream Relay-ből → Relay–Relay kommunikáció
- M43 (`http.request` broker) = WASM modul kér HTTP-t a host-tól → WASM–külvilág
- A kettő párhuzamos fejlesztési vonal, nem egymás előfeltétele.

---

### Spec előtti döntések és invariánsok

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

**11. pont — secret.envelope_read: design spike, nem v1 ígéret:**
A plaintext secret átadás mechanizmusa (A vagy B verzió) nem eldöntött kérdés —
az M41.2 design spike feladata eldönteni. Az OCI signing flow `crypto.sign`-on megy;
`secret.envelope_read` csak akkor szükséges, ha a modulnak tényleg runtime plaintext
credentialt kell kezelnie. Első körben ez nem blokkolja a capability runtime többi részét.

**9. kérdés kockázatjelölés:** az első verzióban csak `crypto.sign` engedélyezett.
`secret.envelope_read` a manifest schema-ból ki van zárva, amíg az M41.2 spike le nem zárja
az A vs. B döntést (addig nem deklarálható olyan capability, amit a Relay nem tud megtagadni).

---

### Minimális spec-csomag — issue bontás előfeltétele

Nem egy monolit specifikáció kell, hanem 9 rövid döntési spec + 1 design spike.
Ezekből lesz majd rendes issue-k, PR-sorrend és DoD.

| Spec | Típus | Cím | Célmilestone |
|------|-------|-----|-------------|
| A | spec | Execution Profile Spec | M38 |
| B | spec | Capability Manifest Schema | M39 |
| C | spec | Hostcall ABI | M40 |
| D | spec | `crypto.sign` Spec | M41 |
| E | spec | Key Custody / Vault Adapter Spec | M41 |
| E2 | **spike** | Secret Envelope Design Spike (A vs. B döntés) | M41.2 |
| F | spec | `http.request` Broker Spec | M43 |
| G | spec | Error + Audit Spec | M44 + M45 |
| H | spec | Echo + OCI Reference Provider Spec | M45.5 + M46 |
| I | spec | Git Policy Migration Spec | M40.5 |

---

### Milestone táblázat (M38–M49)

| Milestone | Cím | Státusz | Megjegyzés |
|-----------|-----|---------|-----------|
| M38 | Execution profile split | concept | Spec A szükséges |
| M39 | Capability manifest schema | concept | Spec B szükséges |
| M40 | Generic hostcall ABI | concept | Spec C; M28 git host functions mintájára |
| M40.5 | Git host functions policy-migration | concept | M28 git hostcall policy engine alá vonása; bypass-útvonal megszüntetése |
| M41 | `crypto.sign` capability | concept | Spec D+E; Vault Transit (M13 minta) |
| M41.2 | Secret Envelope Design Spike | concept | Spec E2; A vs. B verzió eldöntése; v1-et nem blokkolja |
| M42 | Egységes error model | concept | Spec G (hiba rész) |
| M43 | `http.request` broker | concept | Spec F; M37-től független vonal |
| M44 | Limits / budget | concept | Spec G (limit rész) |
| M45 | Audit / ProofTrace integráció | concept | Spec G (audit rész); M17/M22/M32 bővítés |
| M45.5 | Echo reference provider | concept | cloud-független ABI/limit/audit bizonyítás OCI előtt |
| M46 | OCI reference provider | concept | Spec H; `examples/providers/oci/`; M45.5 után |
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
- `ExecutionProfile` / manifest viszony:
  - manifest hiánya = canonical
  - manifest jelenléte explicit `execution.profile` nélkül = **invalid** (fail-fast)
  - capability csak `execution.profile: capability` esetén engedett

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
  git:                          # opcionális; csak ha a modul git host functiont hív
    allow:
      - op: status
        mode: read
      - op: diff
        mode: read
      - op: log
        mode: read
      - op: commit
        mode: write
        requireAudit: true
      - op: push
        mode: write
        requireAudit: true

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

**Tiltott a manifest schema-ban (v1 — parse error, nem silent ignore):**
- `secret.envelope_read` — **nem valid mező v1-ben**; nem forward-compatible placeholder;
  csak M41.2 design spike lezárása után kerülhet vissza
- `relay.kv.*` — concept
- wildcard host (`*`) — alapból tiltott

**Validáció két szinten:**
```text
Manifest schema validation (parse/load time):
  mezők, típusok, enumok, wildcard tiltás, secret.envelope_read tiltott mező

Policy resolution (load/runtime):
  keyHandle létezik-e → load-time fail (nem runtime surprise)
  keyHandle engedélyezett-e ehhez a modulhoz → load-time deny
  algorithm engedélyezett-e a keyHandle-höz → load-time deny
```

**DoD:**
- Hibás schema → manifest parse fail, nem silent ignore
- Unknown capability → parse fail
- Wildcard host → parse fail
- Unknown keyHandle → **load-time policy resolution fail** (nem runtime deny)
- Nem engedélyezett keyHandle/algorithm → load-time deny

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

Válasz (siker):
```json
{
  "status": 0,
  "traceId": "...",
  "output": {}
}
```

Válasz (hiba) — már az ABI szintjén kötelező forma:
```json
{
  "status": 1,
  "traceId": "...",
  "error": {
    "code": "CAPABILITY_DENIED",
    "message": "...",
    "capability": "relay.crypto.sign"
  }
}
```

**Capability-k v1:**
```text
relay.http.request
relay.crypto.sign
relay.clock.now   ← csak capability profile-ban; minden hívás ProofTrace-be kerül;
                     recorded value forma:
                     {"capability":"relay.clock.now","value":"2026-06-18T12:00:00Z",
                      "source":"host","traceId":"...","invocationIndex":0}
                     replay/verify szabályok:
                       - hívássorrend szerint kell visszaadni (invocationIndex)
                       - több vagy kevesebb hívás a trace-ben → verify fail
                       - nincs recorded value → verify fail
relay.trace.emit
relay.git.*       ← meglévő M28 git host functions; M40.5-ben policy alá vonva
```

**Tervezett (M41.2 design spike után):**
```text
relay.secret.envelope_read  ← v1-ben nem valid manifest mező
relay.random.bytes
```

**Concept (külön sprint):**
```text
relay.kv.get / relay.kv.put
```

**DoD:**
- Minden hostcall ugyanazt az envelope formátumot használja
- Minden válasz tartalmaz `status`, `traceId`; hiba esetén `error.code` + `error.capability`
- canonical profile alatt bármely hostcall → `CAPABILITY_DENIED` (az ABI szintjén)
- ABI fuzz / boundary tesztek

---

#### M40.5 — Git host functions policy-migration

**Cél:** a meglévő M28 git host functions ugyanazon capability policy engine alatt
fussanak, mint az új http/crypto capability-k. Különben bypass-útvonal marad.

**Scope:**
```text
relay.git.status
relay.git.diff
relay.git.log
relay.git.commit   ← magasabb kockázati osztály (write)
relay.git.push     ← magasabb kockázati osztály (write)
relay.git.fetch / pull
```

**Policy szabályok:**
```text
canonical profile alatt relay.git.* → ErrCapabilityDenied
capability profile alatt: csak explicit manifest engedéllyel
read-only műveletek (status/diff/log) → külön allowlist
write műveletek (commit/push) → magasabb kockázati osztály, külön engedély
```

**DoD:**
- canonical profile alatt git hostcall → `ErrCapabilityDenied`
- capability profile alatt manifest nélküli git hívás → `ErrCapabilityDenied`
- read vs. write műveletek policy-szinten szétválasztva
- meglévő M28 golden tesztek nem törnek (backward compat)

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
{
  "keyHandle": "oci-prod-api-key",
  "algorithm": "rsa-sha256",
  "purpose": "oci.request-signing",
  "payload": "base64..."
}
```
`purpose` v1-ben audit mező (nem policy gate), de előre lefoglalt — így logban és
auditban nyomon követhető, later policy-hoz felhasználható.

Válasz:
```json
{ "signature": "base64...", "keyVersion": "v3", "traceId": "..." }
```

**Signing-oracle abuse limitek (per keyHandle):**
```text
allowedAlgorithms:   manifest allowlist
maxSignPayloadBytes: manifest limit (default: 32768)
maxSignRequests:     manifest limit (default: 50 / invocation)
```
`crypto.sign` nem általános signing oracle — ezek a korlátok a manifestből jönnek,
nem globálisak.

**Vault adapter döntések:**
- keyHandle → Vault key mapping: Relay konfigurációban
- Ki definiálhat keyHandle-t: csak Relay operator
- Vault hiba: `ErrCapabilityBackendUnavailable`, **nincs silent allow**
- Signature cache: első körben nem

**Signer metadata döntés (v1 = A verzió):** a WASM-nak OCI Authorization headerhez
nem-secret azonosítókra is szüksége van (tenancy OCID, user OCID, fingerprint, region).

```text
v1 döntés: manifest/config tartalmazza (A verzió)
  tenancy_ocid, user_ocid, fingerprint, region — nem titkok, statikus konfig
```

`relay.crypto.signer.describe` hostcall **nem kerül v1-be** — az appendixben
"tervezett" státuszban marad. Kevesebb új capability, egyszerűbb v1 scope.
A `crypto.sign` csak aláír — signer metadata a manifestből jön.

Audit események: `crypto.sign.requested`, `crypto.sign.allowed`, `crypto.sign.denied`

**DoD:**
- `secret.read` nincs
- Csak engedélyezett keyHandle + algorithm
- Aláírás auditálva payload hash-sel (nem nyers payload dump)
- unknown keyHandle → `ErrCapabilityKeyDenied`
- wrong algorithm → `ErrCapabilityAlgorithmDenied`
- payload too large → `ErrCapabilityLimitExceeded`
- Vault unavailable → `ErrCapabilityBackendUnavailable`
- canonical profile alatt crypto.sign → `ErrCapabilityDenied`

---

#### M41.2 — Secret Envelope Design Spike

**Cél:** eldönteni, hogy a `secret.envelope_read` capability hogyan valósítható meg
úgy, hogy a biztonsági invariáns konzisztens legyen. **Ez nem implementációs milestone,
hanem research/design spike** — kimenete egy döntés és egy spec, nem kód.

**Eldöntendő kérdés:** ki végzi az envelope wrappinget?

**A verzió — szigorú no-plaintext Relay (erősebb):**
```text
Vault/KMS végzi a wrappinget a modul recipientPublicKey-re.
Relay csak ciphertextet kap és továbbít.
Relay plaintextet soha nem lát.
```
Flow:
```text
WASM → hostcall(secretHandle, recipientPublicKey) → Relay → Vault
                                                              ↓ wrap(secret, recipientPublicKey)
                                                            ciphertext ← Vault
WASM ← ciphertext (Relay csak átengedi)
WASM: decrypt(ciphertext, myPrivateKey) → plaintext
```
Előfeltétel: Vault Transit wrap/unwrap API, vagy KMS key wrapping support.

**B verzió — Relay trusted broker (egyszerűbb, gyengébb):**
```text
Relay lekéri a secretet Vaultból (plaintext átmenetileg Relay memóriájában).
Relay enkriptálja recipientPublicKey-re.
Relay visszaadja a ciphertextet.
```
Elfogadható, ha a Relay trusted execution environment-ben fut és a
plaintext nem logolódik, nem tárolódik, nem kerül audit eventbe.

**C verzió — halasztás (v1-re ajánlott):**
```text
secret.envelope_read nem része a v1 capability runtime-nak.
crypto.sign lefedi az OCI signing use case-t.
Design spike lezárása után kerülhet v2-be.
```

**Javasolt döntés: C verzió a v1-hez**, A vagy B megvalósítás v2+ scope.

**v1 capability runtime scope (M41.2 nélkül is teljes):**
```text
execution profile + manifest schema + hostcall ABI
crypto.sign + http.request + limits + audit
OCI reference provider
policy test matrix + CI gate
```

**Spike kimenete:**
- A vs. B döntés dokumentálva
- Ha A: Vault wrap API elérhetőségének ellenőrzése
- Ha B: Relay trusted env. követelmények dokumentálva
- secret.envelope_read manifest schema tervezet
- `secret_envelope_client.go` (WASM) és `cmd/relay/secret_host_funcs.go` (Relay) interface vázlat

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

**Signed header invariáns (crypto.sign + http.request szétválasztás):**
```text
A Relay nem módosíthat csendben aláírt szemantikai headert a crypto.sign után.
Provider signing flow-ban a WASM köteles a Relay canonical host szabályai szerint
előállítani a signing stringet.
A Relay validál — nem javít.
Eltérés esetén: deny.
```

**Egress policy v1 — kötelező szabályok:**
```text
scheme:        csak https (http → deny)
host:          exact match, canonical lowercase; trailing dot normalizálva
port:          default 443 vagy explicit allowlist
wildcard host: tiltott
IP literal:    tiltott alapból (explicit operator allowlist esetén engedett)
redirect:      3xx válasz visszaadható a WASM-nak, de auto-follow tiltott;
               új hostra/methodra a Relay nem megy tovább; WASM dönt
proxy env:     ignorálva (HTTP_PROXY / HTTPS_PROXY nem érvényes)
DNS rebinding: private/link-local/loopback range SSRF-védelem, default-on:
               127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16,
               169.254.0.0/16, ::1, fc00::/7, fe80::/10 → deny
               explicit operator allow-listtel felülírható
```

**Header policy:**
```text
redaction (log/audit szinten kötelező):
  authorization — átmehet outbound requestben ha manifest engedi az adott hosthoz,
                  de log/trace/audit szinten mindig redacted
  x-vault-token — mindig redacted, soha nem mehet ki
  date          — átmehet; clock.now-ból származó érték esetén trace-ben rögzíteni kell

hop-by-hop (tiltott kimenő):   Connection, Transfer-Encoding, Keep-Alive, Upgrade
proxy-related (tiltott):       Proxy-Authorization, X-Forwarded-For, X-Real-IP
Host header:                   Relay ellenőrzi, hogy egyezik-e a canonical allowlist hosttal;
                               eltérés esetén deny — nem csendes felülírás
```

**DoD:**
- Nem engedélyezett host → `ErrCapabilityHostDenied`
- Nem engedélyezett method → `ErrCapabilityDenied`
- http scheme → `ErrCapabilityDenied`
- redirect attempt → `ErrCapabilityDenied`
- IP literal → `ErrCapabilityHostDenied`
- body too large → `ErrCapabilityLimitExceeded`
- Authorization header nem jelenik meg logban (redaction teszt)
- Request/response trace keletkezik trace_id-val

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

#### M45.5 — Echo reference provider

**Cél:** cloud-független, deterministic test-mode WASM modul injected clockkal,
mock signerrel és mock transporttal. Bizonyítja a capability ABI-t, a limiteket,
az auditot és a redaction-t OCI/Vault függőség nélkül — ez a "hello world" a
capability runtime-nak, OCI előtt kötelező.

**Modul felelőssége:**
```text
relay.clock.now → injected deterministic clock (nem system clock)
relay.crypto.sign mock → determinisztikus fake signature: "mock:" + sha256(payload);
                          nem valódi private key, nem Vault, nem crypto trust primitive
relay.http.request mock echo endpoint → request visszatükrözés mock transport-on
relay.trace.emit → audit event küldés
```

**Mit bizonyít:**
- Hostcall ABI működik end-to-end
- Limits érvényesülnek (request count, body size, timeout)
- Redaction működik (Authorization header nem jelenik meg logban)
- Audit event keletkezik minden capability hívásra
- canonical profile alatt minden hívás deny

**DoD:**
- Injected clock, mock signer, mock transport — nincs Vault, nincs hálózat
- Minden capability-t tesztel legalább egy positive és egy negative esettel
- OCI reference provider (M46) erre az alapra épül

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
  identity_compartments.go   ← első demo: listCompartments
  compute_instances.go       ← második demo
```

**WASM-ban van:** OCI endpoint mapping, signing string canonicalization,
Authorization header construction, response normalization.

**WASM-ban nincs:** Vault API kliens, private key, OCI credential plaintext, direct socket/TLS.

**Első demo: `listCompartments`** (identity endpoint, GET, egyszerű normalized output,
könnyebb signing-string golden teszt). `listInstances` (compute_instances.go) a második demo.

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

**Threat model review gate (v2.0 előfeltétel):**
```text
SSRF                 — egress policy + private range deny lefedi?
secret exfiltration  — crypto.sign soha nem ad ki kulcsot; secret.envelope_read döntés zárva?
signing oracle abuse — per-keyHandle limit + purpose mező + maxSignRequests érvényes?
audit tampering      — ProofTrace chain_hash integritás; recorder commit protected?
replay divergence    — clock.now invocationIndex; verify fail ha eltér?
capability escalation— manifest parse fail unknown capability-re; load-time policy resolution?
git write abuse      — requireAudit: true push/commit; canonical profile deny?
```

DoD: minden fenyegetéshez deny/audit/test mapping dokumentálva; nincs nyitott SSRF-vektor.

---

### Javasolt verziósorrend

```text
v1.0  = canonical-only baseline (a hivatalos milestone doksi és CI állapot szerint)
v1.1  = capability schema + disabled runtime (M38–M39)
v1.2  = hostcall ABI + git policy migration (M40 + M40.5)
v1.3  = crypto.sign mock + tesztek (M41 + M42)
v1.4  = http.request broker mock + tesztek (M43 + M44)
v1.5  = Vault Transit adapter + audit (M41 éles + M45)
v1.6  = Echo reference provider (M45.5)
v1.7  = OCI reference provider (M46)
v2.0  = production capability runtime (M47–M49)
```

---

## Appendix: non-v1 candidate capabilities

Ezek a capability-k **nem részei az M38–M48 v1 scope-nak**. Manifest schema-ban
nem valid mezők, amíg a megfelelő design spike le nem zárul.

| Capability | Állapot | Feltétel |
|-----------|---------|---------|
| `secret.envelope_read` | concept | M41.2 design spike (A vs. B döntés) |
| `relay.random.bytes` | tervezett | M41.2 után, ha szükséges |
| `relay.kv.get` / `relay.kv.put` | concept | külön sprint, KV store design |
| `relay.crypto.signer.describe` | tervezett | M41 signer metadata döntéstől függ |

---

## Sync-megjegyzés

Ezt a fájlt az AI-támogatott beszélgetések frissítik.
A hivatalos, részletes forrás: `docs/hu/roadmap/relay_milestones.md`