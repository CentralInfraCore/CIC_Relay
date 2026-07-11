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
| `executor` port + `http-executor` (végrehajtó-obj) | **implemented** (bekötetlen) | `module.open` → executor pipeline; FFI runtime bridge |

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
| p60  | —         | ✅ Done   | Build-provenance hardening: `deadcode`+python image digest pin, `release-relay` VERSION guard, `BUILDER_IMAGE_DIGEST` single-source (drift javítva), pip `--require-hashes`, Python CI gate (ruff/black/isort/mypy/yamllint/bandit/pytest) + `pytest.ini`/`.coveragerc`, compose konszolidáció (`go-*`/`py-*` service-ek), `fetch-pki-release` (release-ben tls-skip-verify tiltva), `source -> .` symlink eltávolítva |
| p61  | —         | 🟢 Todo   | `deadcode` valódi gate-té tétele: jelenleg exit 0-val fut (informatív), 56 találat nagyrészt teszt-only exported függvény (deadcode `-test` nélkül tévesen elérhetetlennek látja). Kell: `-test` flag vagy allowlist, hogy a valós dead code bukjon, a teszt-only false-positive-ok ne. Utána CI/Makefile hard gate. (p60 follow-up) |
| p62  | —         | 🟢 Todo   | `ai/PROMPTMAP.yaml` valódi kulcs-duplikációk (7×: `prompt`, `tests`, `sprint`, `tasks`) — YAML-ben néma felülírás → adatvesztés-kockázat. Tartalmi review + struktúra-javítás (külön dokumentumok vagy lista-elemek), majd yamllint-scope-ba visszavétel. (p60-ban felszínre került) |
| p63  | —         | ✅ Done   | Rust build-provenance scaffold, a Go/Python-nal azonos szigorral: gyökér Cargo workspace + első `connections` lib crate, `rust-toolchain.toml` (1.96.1 pin + rustfmt/clippy), `rust:1.96.1-bookworm` digest-pin (`&rust_image` anchor → `RUST_IMAGE_DIGEST` single-source), `Cargo.lock` + `--locked`, `deny.toml` (cargo-deny supply-chain gate), `rust-builder` service, Makefile-célok (`fmt-rust`/`lint-rust` clippy -D/`test-rust`/`deny-rust`/`release-rust`/`rust`) + integrálva `fmt`/`lint`/`test`/`check`-be, `rust` CI-job (fmt/clippy/test/deny) |
| p64  | —         | ✅ Done   | Rust coverage-gate: `cargo-llvm-cov` + `--fail-under-lines` (RUST_COV_MIN=90 floor; `connections` 100%), `llvm-tools-preview` a toolchainbe, `coverage-rust` Makefile-cél a `rust` umbrellában, coverage-lépés a `rust` CI-jobban — paritás a Go/Python coverage-kapukkal. (p63 follow-up) |
| p65  | —         | ✅ Done   | Rust mutációs tesztelés (`cargo-mutants`) — paritás a Go gremlins-gate-tel: `mutate-rust` Makefile-cél (a lassúság miatt a per-push `rust` gate-en kívül) + Rust-lépések a PR-only `mutation` CI-jobban; bukik, ha bármely életképes mutáns túlél (`connections`: 6 caught, 1 unviable, 0 missed). (p63 follow-up) |
| p66  | feature-008 | ✅ Done   | Vault Connection core object-model: `Cn`/`AuthScope`/`SecretRef`/`CounterSignedCert`/`SealedSecret` + typestate `Connection<S>` (`connections/src/vault/mod.rs`) — clippy `-D warnings`, 100% coverage, cargo-deny ok |
| p67  | feature-008 | ✅ Done   | Port/adapter trait surface: `Countersigner`/`TokenProvider`/`SecretBackend` + tipizált hibák, in-memory refs, `establish()`, contract tesztek (`connections/src/vault/port.rs`) — sync/deterministic/dependency-free, 16 teszt, 100% |
| p68  | feature-008 | ✅ Done   | Live Vault backend a `sinkog/vault-client-rs` forkon (rev `17cba8d`, pinned git dep): `VaultSecretSource::read_sealed` KV v2-n, `AdapterError` mapping (`vault-adapter/`) — async read path, coverage 97.85%, `deny.toml` fork-source engedélyezve |
| p69  | feature-008 | ✅ Done   | Vault cert-auth → scoped token: `VaultSecretSource::login` (`cert_at(mount).login(role)`), `IssuedToken` redacting `SecretString`-ként |
| p70  | feature-008 | ✅ Done   | Live-path wiremock integrációs tesztek `read_sealed` + `login` útra (`vault-adapter/tests/live_mock.rs`) — mockolt Vault HTTP, `vault-adapter` 100% line coverage |
| p71  | feature-008 | 🟢 Todo   | Relay countersignature runtime (intermediate-CA + policy) — **blocked**: az intermediate *signing kulcs* runtime-custody hiányzik (verify oldal él, signing oldal nincs). Megerősített blokkoló, döntés: kulcs Vaultból injektálva (`core/nexus/crypto` Transit). Lásd `features/feature-008/spec.md` *Countersign runtime blocker* |
| p72  | feature-008 | ✅ Done   | Vault-lib **driver-level mock** alapozás a p71 custody-hoz (valódi Vault és provisioning-döntés nélkül): a `sinkog/vault-client-rs-test` forkban kézzel írt trait-mockok (`MockKv2`/`MockCertAuth`, `feat/driver-trait-mocks` ág, rev `73dab2c`, test-crate coverage-floorból kizárva); CIC-Relay oldalon szűk `VaultBackend` port + generikus `VaultSecretSource<B>` + `tests/driver_mock.rs` (6 teszt, WireMock nélkül; a p70 HTTP-tesztek megmaradnak). clippy `-D`, deny, coverage (adapter line 100%) zöld. `Transit`/`Pki` mock + Go `CryptoService` fake szándékosan a custody slice-ra halasztva (nincs rá consumer-teszt most) |
| p75  | feature-008 | ✅ Done   | **Pubkey-sealing használat — a kör bezárul** (trust-flow 7. lépés): `module/src/sealing.rs` — `seal(pubkey_der, ref, plaintext)` → `SealedSecret` és `ModuleIdentity::open()` → redaktált `SecretString`. **ECIES P-256 felett** (ephemeral ECDH + HKDF-SHA256 + AES-256-GCM); **spec-korrekció**: sealing = ECIES a modul kulcsára, NEM Vault Transit (az nem tud külső modul-pubkey-re titkosítani). A modul a kulcs-tartó ÉS execution endpoint — a titok a modulon *belül* fejtődik. Teljes-flow e2e (`e2e_countersign.rs`): CSR → countersign → seal → `Connection::initialize` → `module.open(connection.token())`. Depek: `p256/ecdh`+`hkdf`+`aes-gcm`, mind permissive, **nulla deny.toml-változás**. 9 module + 2 e2e teszt (round-trip, wrong-key, tampered, short-blob, invalid-recipient, non-utf8); clippy `-D`, deny, coverage, `make test` zöld |
| p74  | feature-008 | ✅ Done   | **Modul-oldali (WASM) CSR-generálás** (trust-flow 2. lépés): új `module` crate — `ModuleIdentity::generate(subject, scope)` P-256 kulcspárt + PKCS#10 CSR-t (subject CN + scope CN-ek SAN-ként) gyárt → `connections` `CertRequest`; a privkulcs a modulban marad (redaktált `Debug`, sosem lép ki). Pure-Rust (`p256`+`x509-cert`+`sha2/oid`), **permissive-only, nulla deny.toml-változás**. E2e round-trip teszt (`vault-adapter/tests/e2e_countersign.rs`): modul CSR → `VaultPkiCountersigner` (MockPki) → `CounterSignedCert`. clippy `-D`, deny, coverage (TOTAL 95%+), teljes `make test` zöld. Nyitva: SAN-kódolás finomítása (subset-authorizáció), pubkey-sealing használat |
| p73  | feature-008 | ✅ Done   | **Élő countersign runtime** (a `connections` `Countersigner` port futó megfelelője): `vault-adapter/src/countersign.rs` — `CountersignPolicy` döntési pont (a Relay „okossága", tartalomfüggő autorizáció; `AllowlistPolicy` a triviális első impl) + `PkiBackend` mechanizmus (Vault PKI `sign-verbatim`, kulcs Vaultban marad) + `VaultPkiCountersigner` (authorize → sign → `CounterSignedCert` az engedélyezett scope-pal). **Spec-korrekció**: countersign = PKI engine, nem Transit (a Transit a sealinghez marad). Fork `MockPki` (`feat/mock-pki`, rev `57d26ed`, PR #2); tesztek: 5 unit + 4 `countersign_mock` (MockPki) + 1 `countersign_live` (WireMock). clippy `-D`, deny, coverage zöld. Nyitva marad (runtime bridge): WASM-oldali CSR-generálás + privkulcs-custody, élő Vault PKI mount bootstrap |
| p76  | feature-008 | ✅ Done   | **Rust ↔ Go FFI-híd — foundáció (2a+2b): a Rust trust-flow lib-ként a Go relaybe**. Integrációs döntés: a Rust crate-ek **cgo/static linkeléssel** kerülnek a Go relaybe; a szeparáció gyengülését a felhasználó máshol kezeli. **Elvek**: `features/feature-008/ffi-boundary.md` — „unsafe airlock" minta (INGRESS unsafe: másol+validál → CORE safe → EGRESS unsafe: owned buffer + párosított free), 7 kikényszeríthető határszabály + review-checklist. **2a (Rust)**: új `ffi/` crate (`cic-ffi`) — az **egyetlen** crate, ahol `unsafe` engedélyezett (a workspace `unsafe_code = "forbid"` marad a business crate-ekben); saját lint-posztúra `undocumented_unsafe_blocks = "deny"`-nal; `crate-type = ["staticlib","rlib"]`; `cic_ffi_ping`+`cic_ffi_free` a 3-fázisú mintával, `catch_unwind` backstop. fmt + clippy `-D` + 4 teszt + coverage (ffi 98.15%, TOTAL 97.35%) + cargo-deny zöld; `.a` `T cic_ffi_ping`/`cic_ffi_free` néven exportál. **2b (Go cgo + build)**: `pkg/cicffi/` — **build-taggel izolált** (`//go:build ffi_cgo` + stub), így a default `CGO_ENABLED=0` tiszta-statikus build **érintetlen** (igazolva: `go build ./...` CGO-off zöld, tesztek tag mögött); `Ping()` wrapper (`GoBytes`-másolás + `cic_ffi_free`); `docker-compose.yml` rust-builder `./output` mount + Makefile `ffi-staticlib`/`test-ffi` célok (`CGO_ENABLED=1 CGO_LDFLAGS=-L/output/lib`). **Élő Go→Rust→Go round-trip igazolva** (`make test-ffi` 2/2 zöld). **Nyitva (következő)**: az első valódi trust-flow határfüggvény bekötése (countersign/seal a `cic-ffi`-n át) + CGO_ENABLED=1 build-provenance konkretizálása a release-ágban |
| p77  | feature-008 | ✅ Done   | **Első valódi trust-flow függvény a seam-en át** (a `logic::echo` helyett valódi hívás): `cic_ffi_connection_request` — JSON `{subject, scope[]}` → **`connections::vault::Connection::request`** + `AuthScope` (BTreeSet: dedup+sort) → kanonikus JSON vissza. Az unsafe most **egyetlen közös `marshal` airlock-helperben** (INGRESS copy+own → CORE `fn(&[u8])->Option<Vec<u8>>` panic-contained → EGRESS), a `ping` és az új export is ezen megy át; a validáció a seam-en dől el (rossz JSON / üres subject → `None`, sosem panic). Depek: `serde_json` (a lockban volt, permissive, nulla deny-változás). Go-oldal: `pkg/cicffi.ConnectionRequest([]byte) ([]byte, error)` közös `invoke` helperrel. Igazolva: 7 Rust + 4 Go teszt (élő Go→Rust→Go, dedup+sort round-trip), fmt/clippy `-D`/coverage (ffi 99.17%, TOTAL 97.52%)/deny zöld, CGO-off `go build ./...` érintetlen |
| p78  | feature-008 | ✅ Done   | **Countersign scope-invariáns (P1 biztonsági guard, külső review nyomán)**: a `sign-verbatim` az eredeti CSR-t írja alá változatlanul → a kiadott cert SAN-jai = `request.scope`, és a **Vault cert-auth a certre autorizál, nem a `CounterSignedCert.scope` metaadatra**. Egy rész-scope-ot engedő policy így a metaadatot szűkítené, a kriptográfiai valóságot nem → túl-tág cert. Fail-closed: `authorized != request.scope` → **`CountersignError::ScopeNarrowingUnsupported`** (`vault-adapter/src/countersign.rs`), nem `debug_assert` (ami release-ben `panic=abort` mellett eltűnne). `AllowlistPolicy` all-or-nothing, ma teljesül; jövőbeli narrowing-policy előfeltétele, hogy a mechanizmus a **signolt bájtokban** tükrözze a döntést (CSR-újraépítés v. cert-SAN visszaellenőrzés). Teszt: `scope_narrowing_is_refused_before_signing` (subset-policy → guard a backend-hívás előtt). Spec-kontraktus rögzítve. clippy `-D`/coverage (countersign.rs 94.12%, TOTAL 97.72%) zöld |
| p79  | feature-008 | ✅ Done   | **FFI-híd production-keményítés (P2, külső review nyomán)**: (1) **ingress size-cap** (`MAX_INPUT_LEN` 1 MiB) — túl nagy input a seam-en elutasítva, mielőtt másolódna (`ffi-boundary.md` rule 3); (2) **ABI-verzió** — `cic_ffi_abi_version()` export ↔ Go `ExpectedABIVersion`/`CheckABI()`, startup-ellenőrzés a stale/rossz `libcic_ffi.a` link ellen; (3) **Go-oldali hossz-guard** — `outLen` a `math.MaxInt32` ellen ellenőrizve `C.GoBytes` előtt (a review `C.int` overflow-találata). Igazolva: 9 Rust + 6 Go teszt (`make test-ffi`), fmt/clippy `-D`/coverage (ffi 99.27%, TOTAL 97.75%)/deny zöld, CGO-off `go build ./...` érintetlen. **Nyitva (P2b, szándékos)**: strukturált hibakód/status-csatorna (ABI-változás, külön slice) |
| p80  | feature-009 | 📐 Design | **Aláírt, hash-láncolt audit-log (observability-kimenet) — design-szelet** (`features/feature-009/spec.md`, `d/feature-009` ág): többszintű (trace…error), típusos JSON rekordok, **két független hash-lánc** (per-`type` ÉS per-`topic`, `prev_type_hash`+`prev_topic_hash`), a `record_hash` **self-kizáró projekcióval** (`record_hash`+`signature` kihagyva, `metaForChecksum`-minta), `signature = sign(record_hash)` a relay kulcsával (Vault, recorder-minta) → relay-certtel verifikálva. A típus/topic **emitter-definiált** (WASM modulok + libek); az infra egy **generikus, per-(type,topic) szerializált append-szalag**, a relay bélyegzi az integritás-mezőket. Nagyrészt a **`pkg/obs` (M22) kiterjesztése** (`RecordHash`/`TypeChainHash` már megvan). Beeresztő pontok: WASM host-log-ABI (`core/cabinet`) + FFI-seam Go-oldal (feature-008 P3). Nyitva: topic-kardinalitás, aláírás-granularitás, truncation-anchoring, kulcs-custody (=p71) |
| p81  | feature-008 | ✅ Done   | **`executor` crate — a `module/host` páros végrehajtó-felének object-modellje + port** (feature-008 vízió, roadmap **M43-csíra**): `ExecRequest`/`ExecOutcome`/`Executor` trait + in-memory `MockExecutor`. Hiba-taxonómia HTTP-hűen rekonszolidálva: **megérkezett endpoint = `Ok(ExecOutcome)`** bármely statusra (egy `401`/`500` nem hiba, hanem outcome); `ExecuteError` = `InvalidRequest`/`Denied`/`Unavailable`. A credential a `module::open`-nel azonos redaktáló `secrecy::SecretString`, sosem kerül kimenetbe/logba. Pure/sync/dep-light (csak `secrecy`). clippy `-D`, coverage **100% sor**, 8 teszt |
| p82  | feature-008 | ✅ Done   | **`http-executor` — élő async HTTP adapter a `executor` port mögött** (a `vault-adapter`↔`connections` mintára; roadmap **M43-csíra**): `EgressPolicy` (**https-only + host-allowlist**, wildcard tiltott, IP-literál csak explicit; `insecure_allowing` loopback/teszt), `HttpExecutor` `reqwest`-tel (**rustls-only**, redirect auto-follow tiltva), a nyitott credential **`Authorization: Bearer`**-ként alkalmazva, coarse nem-szivárgó hibaképzés. **wiremock** integrációs tesztek (credential ténylegesen kimegy; `401`=Ok-outcome; redirect nem követve; body-forward; unreachable→`Unavailable`). Supply-chain: **nulla új C-toolchain/licenc-teher**, `reqwest` 0.13.4 marad, minimális Cargo.lock-diff. clippy `-D`, coverage 96.1% (TOTAL 98.1%), deny zöld, 11 teszt. **Nyitva**: `module.open`→executor e2e pipeline, további M43 egress-szabályok (hop-by-hop/proxy header-tiltás, SSRF private-range deny) |
| p83  | feature-008 | ✅ Done   | **`http-executor` security-keményítés (M43/M44-csíra, review nyomán)** — a slice-2 négy 🔴 rése bezárva valódi forgalom előtt: (1) **request/connect timeout** (`Limits`, default 30 s/10 s — nincs többé végtelen lógás); (2) **response-body cap** (default 1 MiB, `Content-Length` elő-ellenőrzés + inkrementális `chunk`-olvasás → `LimitExceeded`); (3) **proxy-env kikapcsolva** (`no_proxy` — a `HTTP(S)_PROXY` nem téríthet el); (4) **SSRF private-range guard** — IP-literál targetre policy-időben `Denied`, hostnévre custom `reqwest::dns::Resolve` a connect-úton szűri a tiltott tartományokat (`127/8`, `10/8`, `172.16/12`, `192.168/16`, `169.254/16`, `::1`, `fc00::/7`, `fe80::/10`, IPv4-mapped is), TOCTOU-mentesen. Új core-variáns: `ExecuteError::LimitExceeded`. Doc: `features/feature-008/http-executor-hardening.md`. clippy `-D`, coverage (http-executor 94.3%, executor 100%), deny zöld, **20 teszt**. **Nyitva marad**: method-allowlist, hop-by-hop/proxy header-higiénia, Host-header validáció, audit/trace + redaction-teszt, per-invocation budget, konfigurálható credential-elhelyezés |

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