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
| p55  | —         | 🟡 1      | Go 1.24.6 → 1.25.x toolchain upgrade; govulncheck hard gate feloldása CI-ban |
| p56  | M37       | 🟢 2      | `UpstreamSource` HTTP implementáció (relay federáció v2 előkészítés) |

---

## Tervezett milestone-ok (részletes)

### M30–M31 — Done (archívum)
Részletek: `ROADMAP_archive.md`

---

## Sync-megjegyzés

Ezt a fájlt az AI-támogatott beszélgetések frissítik.
A hivatalos, részletes forrás: `docs/hu/roadmap/relay_milestones.md`