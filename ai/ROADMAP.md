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
| M28       | **Tervezett** | Git Host Functions: wazero git primitívek WASM moduloknak |
| M29       | **Tervezett** | GitSource: IaC konfig betöltés git repóból |

---

## Scaffold / Bridge térkép

| Komponens | Státusz | Előfeltétel |
|---|---|---|
| `UpstreamSource` (HTTP IaC source) | scaffold | relay federáció v2 |
| `LocalWorker` in-process | scaffold | 3-process isolation + Vault |
| `cicSign` / `cicSignedCA` mezők | scaffold | Layer 2 — CICSourceCA signing |
| quorum döntési réteg | concept | nincs runtime megfelelő |
| CICmeta protocol | concept | nincs élő bridge a Relay runtime-hoz |

---

## Sprint 12 — Folyamatban

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p46  | M29       | Todo    | GitSource: IaC konfig betöltés git repóból (relay infrastruktúra) |
| p47  | M28       | Todo    | Git Host Functions: wazero primitívek WASM moduloknak |

---

## Tervezett milestone-ok (részletes)

### M29 — GitSource (IaC konfig betöltés git repóból)
* **Állapot:** Tervezett
* **Fő cél:** Az IaC konfiguráció betöltése git repóból, relay infrastruktúraként. A `GitSource` implementálja az `IaCSource` interfészt — clone-or-pull logikával szállítja a YAML fájlokat a loadernek. Ez relay-szintű infrastruktúra (nem WASM modul): a git csak IaC konfig forrásként jelenik meg, domain logikát nem hordoz.
* **Architekturális döntés:** `os/exec("git", ...)` alapú — konzisztens a `recorder.go` és `syncer.go` mintájával. CGO_ENABLED=0, go-git library nem kerül be.
* **Részfeladatok (checklist):**
    *   [ ] **M29.1**: `core/nexus/iac/source_git.go` — `GitSource` struct, `NewGitSource(cfg GitSourceConfig)`, `FetchConfig(ctx)`:
        - ha a repo nem létezik: `git clone --branch <branch> --depth 1 <url> <path>`
        - ha létezik: `git fetch origin` + `git checkout <branch>`
        - visszaadja a fa tartalmát (`FileSource` logikával)
    *   [ ] **M29.2**: `GitSourceConfig` struct — `URL`, `Branch` (default: main), `LocalPath`, `SSHKeyPath` (opcionális), `Timeout` (default: 30s)
    *   [ ] **M29.3**: Hardened git env: `GIT_CONFIG_NOSYSTEM=1`, `GIT_TERMINAL_PROMPT=0`, `GIT_ASKPASS=/bin/false`, `-c core.hooksPath=/dev/null`; `exec.CommandContext` timeout
    *   [ ] **M29.4**: `cmd/relay/main.go` wiring — `CIC_GIT_SOURCE_URL` env var: ha be van állítva → `GitSource`, különben `FileSource` (backward-compatible)
    *   [ ] **M29.5**: `config/relay.config.schema.yaml` frissítés: `CIC_GIT_SOURCE_URL`, `CIC_GIT_SOURCE_BRANCH`, `CIC_GIT_SOURCE_LOCAL_PATH` dokumentálva
    *   [ ] **M29.6**: Tesztek: bare git repo tmpdir-ben, clone + módosítás + fetch round-trip; timeout teszt; invalid URL → wrapped error
* **Előfeltétel:** `IaCSource` interfész (M11, megvan), `FileSource` (megvan)
* **Nem cél ebben a fázisban:** push, commit, SSH agent (csak olvasás kell az IaC forráshoz)

### M28 — Git Host Functions (wazero primitívek WASM moduloknak)
* **Állapot:** Tervezett
* **Fő cél:** A relay wazero hostban typed git primitívek exportálása, hogy WASM modulok git műveleteket végezhessenek anélkül, hogy tetszőleges parancsot adhatnának ki. A WASM modul csak a host által explicit engedélyezett műveleteket hívhatja — ez adja a valódi kontrollt.
* **Architekturális elv:**
    - `wasm → host git API (typed)` → `os/exec git` (hardened env)
    - A WASM nem kap általános `exec`-et, csak: `git.clone`, `git.fetch`, `git.commit`, `git.push`, `git.status`
    - Repo whitelist: a WASM csak előre regisztrált `repoID`-val hivatkozhat repóra
* **Részfeladatok (checklist):**
    *   [ ] **M28.1**: `core/nexus/git/gitops.go` — `GitOps` interfész + `StatusEntry` típus
    *   [ ] **M28.2**: `core/nexus/git/exec_gitops.go` — `ExecGitOps`: `runGit(ctx, repoPath, args...)` hardened env + context timeout + stdout/stderr méretlimit (1MB)
    *   [ ] **M28.3**: `core/nexus/git/registry.go` — `RepoRegistry`: `Register(repoID, localPath)`, `Resolve(repoID) (string, error)`; ismeretlen repoID → error
    *   [ ] **M28.4**: wazero host function regisztráció — `git.clone`, `git.fetch`, `git.checkout`, `git.commit`, `git.push`, `git.status`; JSON ABI határon
    *   [ ] **M28.5**: Tesztek: clone+status round-trip tmpdir-ben; whitelist enforce; hardened env assert
* **Előfeltétel:** M29 (git exec minta), wazero host function API (M2/cicwasm, megvan)
* **Nem cél ebben a fázisban:** bubblewrap/Landlock/seccomp (külön milestone), SSH agent

---

## Sync-megjegyzés

Ezt a fájlt az AI-támogatott beszélgetések frissítik.
A hivatalos, részletes forrás: `docs/hu/roadmap/relay_milestones.md`