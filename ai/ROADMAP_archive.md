# Relay Roadmap — Archívum

Lezárt milestone-ok részletes leírásai és régi sprint táblák.
→ Aktív roadmap: [ROADMAP.md](ROADMAP.md)

---

## Lezárt sprint táblák

### Sprint 11 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p43  | M22.6     | Done    | Obs Relay Wiring: setHandler TraceFrame + LogRecord + CounterPoint + CaptureSink |
| p44  | M22.7     | Done    | LogrusSink: obs → logrus JSON bridge; minden /set emisszió látható a log streamben |
| p45  | M27       | Done    | L0.4: metadata.checksum spec content cross-validation (sha256 canonical_json) |

### Sprint 10 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p41  | M22       | Done    | Observability Layer Phase 1: pkg/obs alaptípusok + hash-lánc + sink interfészek |
| p42  | M26       | Done    | L0.3 Source Assertion Validator: CertVerifyFunc factory + X.509 chain verify + tesztek |

### Sprint 9 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p38  | M23       | Done    | IaC Schema Integration: ResourceSpec + IaCValidator + Service operator + testdata |
| p39  | M24       | Done    | PoC Execution Bridge: CabinetActivator + dependency gating |
| p40  | M25       | Done    | PKI Trust Bootstrap: embedded dev CA + CIC_TRUST_MODE + pki_verify.go aktív |

### Sprint 8 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p36  | M20       | Done    | SourceDigest upgrade: source tree hash (pkg/sourcedigest + tools/sourcehash + SourceTreeHash ldflags) |
| p37  | M21       | Done    | Builder image digest-pinning: docker-compose.yaml + CI Go verzió + staticcheck pinelve |

### Sprint 7 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p33  | M9        | Done    | Timestamp kivétele release ldflags-ből — RELEASE_LD_FLAGS |
| p34  | M9        | Done    | cyclonedx-gomod @latest → pinelt verzió |
| p35  | M2        | Done    | registerNativeComponents() behívása main() runtime útvonalán |

### Sprint 6 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p29  | M17       | Done    | ProofTrace.SourceDigest + computeChainHash anchoring |
| p30  | M18       | Done    | PollingWatcher scanDir + emitDiff (CREATE/UPDATE/DELETE) |
| p31  | M19       | Done    | ci.build@1.0 native Go modul + schema |
| p32  | M3        | Done    | PKI WrongChain e2e teszt (in-process ECDSA cert gen) |

### Sprint 5 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p25  | M9        | Done    | SBOM release artifact (CycloneDX) |
| p26  | M14       | Done    | Audit sync integrációs tesztek |
| p27  | M15       | Done    | SubprocessWorker IPC + RunWorkerLoop |
| p28  | M16.4     | Done    | Operator + Watcher bekötve main.go-ba |

### Sprint 4 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p20  | M7.2-M7.4 | Done    | CI quality gates: lint/vet/coverage/HTML report |
| p21  | M4.4      | Done    | LIMITS schema config (CIC_WASM_EXEC_TIMEOUT_SECONDS) |
| p22  | M9        | Done    | Rate limiter + concurrent cap + slow-loris |
| p23  | M16.2-3   | Done    | Operator bootstrap + lifecycle (HMAC token) |
| p24  | M11       | Done    | UpstreamSource HTTP implementáció + retry/backoff |

### Sprint 3 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p15  | M12       | Done    | Git state recorder + WorkflowRecorder |
| p16  | M13       | Done    | Vault Transit signing |
| p17  | M14       | Done    | Audit sync squash/push/prune |
| p18  | M15       | Done    | 3-process isolation coordinator |
| p19  | M16.1     | Done    | PollingWatcher skeleton |

### Sprint 2 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p7   | M8.3      | Done    | Prod recorder wiring (Vault + Git + main.go) |
| p8   | M11       | Done    | IaC Source + Loader + Graph builder |
| p9   | M6.3-6.4  | Done    | /healthz /readyz + API handler tesztek |
| p10  | M9        | Done    | Release artifact chain alap |
| p11  | M5        | Done    | Egységes hibamodel |
| p12  | M4        | Done    | WASM méretlimit refactor |
| p13  | hygiene   | Done    | Embedded PKI key audit |

### Sprint 1 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p1   | M2        | Done    | cabinet-canonicalize |
| p2   | hygiene   | Done    | fix-audit-placeholder-log |
| p3   | M2        | Done    | schema-validation-setx |
| p4   | M2        | Done    | proof-trace-type |
| p5   | M2        | Done    | setx-recorder-wire |
| p6   | M2        | Done    | execution-graph-linear |

---

## Lezárt milestone-ok részletes leírásai

### M-Konfig — Séma-Vezérelt Önvalidáció ("Dogfooding")
* **Állapot:** Done (M8.1 + M8.2 részeként lezárva)
* **Fő cél:** A rendszer induláskor a saját séma-validációs képességeit használja önmaga konfigurációjának érvényesítésére.
* **DoD:** `cabinet.ValidateSchema` exportálva; `validateConfig` a main-ben; `config/relay.config.schema.yaml` dokumentálja az összes CIC_* változót; hibás konfig → Fatal.

### M0 — Tesztek refaktorálása (dinamikus checksum)
* **Állapot:** Done
* **DoD:** A tesztek dinamikus `cabinet.New...` konstruktorokat hívnak statikus hash értékek helyett.

### M1 — WASM checksum „készre"
* **Állapot:** Done
* **DoD:** `stripWasmModuleFunc` (`wabin`); `TestCanonicalWasmGolden` — azonos WASM → azonos hash; CAS-alapú ModuleStore.

### M2 — Schema/Workflow betöltés és CabinetService
* **Állapot:** Done
* **DoD:** `ExecutionGraph` lineáris + névvel ellátott kimenet-kulcsok; `Setx` schema validáció (`moduleDesc.Accepts`); `ProofTrace` per-step hash + chain_hash; `make test` + `make verify` zöld.

### M3 — Privát PKI verifikáció
* **Állapot:** Done
* **DoD:** `bootstrap_testdata.sh` + go:embed; `VerifyCertificateChain`; EKU=CodeSigning; PKIVerified/PKIRejected tesztek.

### M4 — LIMITS érvényesítés
* **Állapot:** Done
* **DoD:** JSON 413, WASM méretlimit, ErrTimeout/IsTimeout, `CIC_WASM_EXEC_TIMEOUT_SECONDS` (≥1, default 30), range check a sémában.

### M5 — Hibamodel egységesítése
* **Állapot:** Done
* **DoD:** `writeError` helper; `{"error":"...","code":"..."}` minden útvonalra; `errors.Is/As` működik.

### M6 — Build & Release Chain
* **Állapot:** Done
* **DoD:** Strukturált log (req-id, latency, outcome, checksum); /healthz /readyz; API handler tesztek; `api_tests/smoke.sh`.

### M7 — Supply-chain Validation / CI Quality
* **Állapot:** Done
* **DoD:** SYMBOLS.md drift CI; staticcheck + go vet CI; per-pkg coverage thresholds; HTML coverage artifact.

### M8 — Observability / Config
* **Állapot:** Done
* **DoD:** `CIC_` env prefix; dogfooding config (`relay.config.schema.yaml`); `loadSecretsFromVault` szétválasztva.

### M9 — Operations Safety + Release Artifact Chain
* **Állapot:** Done
* **DoD:** Per-IP token bucket (429); concurrent semaphore (503); slow-loris timeouts; `make release` statikus bináris + sha256 + CycloneDX SBOM; `RELEASE_LD_FLAGS` (timestamp nélkül); cyclonedx-gomod pinelve.

### M10 — Enterprise Rollout Features
* **Állapot:** Done (M9-be olvadt)

### M11 — IaC Feldolgozás és Gráf Építés
* **Állapot:** Done
* **DoD:** `IaCSource` interfész; `FileSource` (rekurzív fájl olvasás); `UpstreamSource` (HTTP GET, JSON/YAML auto-detect, retry/backoff); `Loader` (Relay→Host→Service graph); cross-ref validáció.

### M12 — Git-alapú Állapotrögzítés (Nexus Recorder)
* **Állapot:** Done
* **DoD:** `GitStateRecorder` (expected+actual JSON → git commit); `WorkflowRecorder`; `Setx` összekötés; Vault hiba non-fatal.

### M13 — Vault Integráció és Aláírás
* **Állapot:** Done
* **DoD:** Vault Transit wrapper; `make dev-vault`; `buildRecorderOption` a main-ben; VAULT_TOKEN hiánya WARN + nil recorder.

### M14 — Központi Audit Szinkronizáció
* **Állapot:** Done
* **DoD:** Squash + push + prune; 2 ephemeral git repo integrációs teszt; coverage 83.3% (≥70%).

### M15 — Többrétegű Biztonsági Szeparáció
* **Állapot:** Done
* **DoD:** `IPCRequest/IPCResponse` newline-delimited JSON; `RunWorkerLoop`; `SubprocessWorker` (`os.Executable()` + `--mode=module-worker`); round-trip integrációs teszt.

### M16 — GitOps-vezérelt Életciklus (Nexus Operator)
* **Állapot:** Done
* **DoD:** `PollingWatcher`; `BootstrapHost` HMAC-SHA256 token (24h TTL); `OnboardHost` / `OffboardHost`; `CIC_INFRA_REPO_PATH` → goroutine-ban indul; `CIC_OPERATOR_SECRET` hiánya Fatal.

### M17 — Source-Pinned ProofTrace
* **Állapot:** Done
* **DoD:** `ProofTrace.SourceDigest`; `computeChainHash(sourceDigest, steps)`; `CommitHash` build var injektálva setHandler-ből.

### M18 — PollingWatcher State Diff
* **Állapot:** Done
* **DoD:** `scanDir` content hash snapshot; `emitDiff` CREATE/UPDATE/DELETE; első tick = minden fájl CREATE; 4 teszt.

### M19 — CI Build Modul
* **Állapot:** Done
* **DoD:** `config/schemas/ci.build.schema.yaml`; `core/modules/cibuild/cibuild.go` (make exec, 5 perc timeout, artifact SHA-256); regisztrálva `registerNativeComponents()`-ben.

### M20 — Source Tree Hash
* **Állapot:** Done
* **DoD:** `pkg/sourcedigest` determinisztikus tree hash; `tools/sourcehash` CLI; `SourceTreeHash` ldflags; `make sourcehash`; 7 unit teszt.

### M21 — Hermetic Build Boundary
* **Állapot:** Done
* **DoD:** `docker-compose.yaml` image digest-pin; CI Go `1.24.6` exact; `staticcheck@v0.6.1`; SYSTEM_CONTEXT.md "Build-input boundary" tábla.

### M22 — Observability Layer Phase 1
* **Állapot:** Done
* **DoD:** `pkg/obs/types.go` (Resource, ExecutionContext, LogRecord, TraceFrame, Counter/Gauge/Histogram, IntegrityEnvelope); `chain.go` (RecordHash, GlobalChainHash, TypeChainHash); `sink.go` (TraceSink, LogSink, MetricSink, NoopSink); `registry.go` (MetricInstrument, névtér-validáció `relay.*`); unit tesztek.

### M22.6 — Observability Layer Phase 2: Relay Wiring
* **Állapot:** Done
* **DoD:** `relayServer` obs mezők + nil-safe emit helperek; `setHandler` defer TraceFrame + CounterPoint; sikernél LogRecord; `CaptureSink` tesztekhez; 4 obs teszt.

### M22.7 — Observability Layer Phase 3: LogrusSink
* **Állapot:** Done
* **DoD:** `pkg/obs/logrus_sink.go` LogrusSink (RecordTrace Info/Warn, RecordLog level map, metrikák Debug); `main()` inject mindhárom sinkbe.

### M23 — IaC Schema Integration
* **Állapot:** Done
* **DoD:** `ResourceSpec`, `SchemaRef`, `ResourceState`, `ResourceMeta` típusok; `ServiceSpec.Resources []ResourceSpec`; `IaCValidator` (schemaRef → Cabinet.GetSchema); `ResourceValidator` interfész + `WithValidator()`; `onApplyService` per-resource validáció.

### M24 — PoC Execution Bridge
* **Állapot:** Done
* **DoD:** `CabinetActivator`; `state.desired=present` → `Cabinet.Setx()`; dependency gating (`depsReady()`); ProofTrace ChainHash logolva; tesztek mock Cabinet-tel.

### M25 — PKI Trust Bootstrap
* **Állapot:** Done
* **DoD:** `CIC_TRUST_MODE=dev|prod`; `CICDeveloperCA2026.pem` + `rootCA2026.pem` go:embed; `bootstrapPKI()`; `pki_verify.go` aktívan fut `loadModule`-ban; tesztek.

### M26 — L0.3 Source Assertion Validator
* **Állapot:** Done
* **DoD:** `CertVerifyFunc` típus; `NewAssertSourceFunc(verify)` factory; `registerNativeComponents` kap `*PKIAssets`-t; valid lánc / untrusted CA / invalid PEM / nil verifier / schema mismatch tesztek.

### M27 — L0.4 Spec Checksum Cross-Validation
* **Állapot:** Done
* **DoD:** `assertSourceImpl` L0.4 blokk: `artifact.Spec != nil` → `canonicaljson.ToJSON` → sha256 → hex → compare; `specChecksumHex` helper; 4 L0.4 teszt; minden meglévő stub checksum valódira cserélve.
