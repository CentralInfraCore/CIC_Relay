# Relay Roadmap (AI snapshot)

Ez a fájl az AI-támogatott roadmap állapota.
→ Részletes fejlesztői doksi: [docs/hu/roadmap/relay_milestones.md](../docs/hu/roadmap/relay_milestones.md)

---

## Mérföldkövek (prioritási sorrend, részletesen)

### M-Konfig — Séma-Vezérelt Önvalidáció ("Dogfooding")

* **Állapot:** Tervezett
* **Fő cél:** A rendszer induláskor a saját, belső séma-validációs képességeit használja fel önmaga konfigurációjának érvényesítésére. Ez biztosítja a "fail-fast" működést és a konfiguráció megbízhatóságát, miközben a rendszer "eszi a saját kutyatápját".
* **Részfeladatok / Definition of Done (DoD):**
    *   **1. Konfigurációs Séma Definiálása:** Létrejön egy, a rendszer által értelmezhető séma, amely leírja az összes külsőleg konfigurálható paramétert (pl. WASM futtatási limitek), azok típusait, és az elvárt struktúrát.
    *   **2. Induláskori Önvalidáció:** A rendszer indítási folyamatának részévé válik a saját konfigurációjának betöltése és a fent definiált séma alapján történő validálása. Érvénytelen vagy hiányos konfiguráció esetén a rendszer egyértelmű hibaüzenettel, azonnal leáll.
    *   **3. Konfiguráció Megfigyelhetősége:** A rendszer által használt, validált konfiguráció (az érzékeny adatok, pl. titkok, maszkolásával) elérhetővé válik megfigyelhetőségi célokból, segítve az üzemeltetést és a hibakeresést.
    *   **4. Tesztelés:** Automatizált tesztek biztosítják a konfiguráció-validálási logika helyes működését, lefedve a sikeres validációt és a hibás konfiguráció miatti elvárt leállást is.

### M0 — Tesztek refaktorálása (dinamikus checksum)
* **Állapot:** Kész
* **Feladatok**: A `bootstrap_test.go` tesztjeinek teljes körű átalakítása, hogy a beégetett, statikus checksum-értékek helyett a program saját, kanonikus checksum-számítási logikáját (`cabinet.NewSchema`, `cabinet.NewWorkflow`, `cabinet.NewModuleDescriptor`) használják a tesztadatok előállításához.
* **DoD**: A tesztek már nem tartalmaznak statikus hash-értékeket egyetlen komponens (séma, workflow, modul) esetében sem. A tesztek a `cabinet` csomag `New...` konstruktorait hívják a helyes checksum dinamikus generálásához, így a tesztek ellenállóbbak a jövőbeli változásokkal szemben. A build sikeres.

### M1 — WASM checksum „készre”
* **Állapot:** Kész
* **Feladatok:** A cél egy olyan kanonikus checksum-eljárás létrehozása, amely garantálja, hogy a funkcionálisan azonos WASM modulok, függetlenül a fordítási környezettől, azonos hash-t kapjanak.
* **Részfeladatok (checklist):**
    *   **M1.1: Méret- és CAS-alapú validáció**
        *   [x] **Feladat**: Implementálni egy mechanizmust, amely korlátozza a bejövő JSON payload és a WASM modulok méretét.
        *   [x] **DoD**: Túlméretes input esetén a rendszer hibával (pl. 413) válaszol. A limitek konfigurálhatók.
        *   [x] **Feladat**: Tényleges CAS (Content-Addressable Storage) implementálása a modulok tárolására, a tartalom-alapú címzés kihasználására.
        *   [x] **DoD**: A modulok tárolása a kanonikus checksum-uk alapján történik, elkerülve a duplikátumokat.
    *   **M1.2: WASM Kanonizálás**
        *   [x] **Feladat**: A `stripWasmModuleFunc` függvény implementálása a `wabin` segítségével, amely eltávolítja a nem determinisztikus `CustomSection`-öket.
        *   [x] **DoD**: A `strip` logika kimenete a kanonizált bájtsorozat, amely a determinisztikus checksum-számítás alapja.
    *   **M1.3: Determinisztikus Checksum Igazolása**
        *   [x] **Feladat**: `TestCanonicalWasmGolden` teszt, amely igazolja, hogy két funkcionálisan azonos, de eltérő metaadatokkal rendelkező WASM modul kanonizálás után azonos checksumot kap.
        *   [x] **DoD**: A teszt sikeres, és a CI pipeline része.

### M2 — Schema/Workflow betöltés és CabinetService
*   **Állapot:** Kész
*   **Feladatok:** Schema/Workflow betöltés és a CabinetService Put*/Get* útvonalak megvalósítása, teljes végrehajtási útvonallal.
* **Részfeladatok (checklist):**
        *   [x] **Feladat**: A `wasmDigest` (sha256) és `wasmSize` mezők kötelezővé tétele a modul YAML-definíciójában.
        *   [x] **Feladat**: A `loadModule` függvény kiegészítése a `wasmSize` és `wasmDigest` ellenőrzésével.
        *   [x] **Feladat**: A kapcsolódó tesztek frissítése és létrehozása.
        *   [x] **Feladat**: WASM és native Go végrehajtási útvonal bekötése (`Setx`, `WasmManager`, `ExecutionGraph`).
        *   [x] **Feladat**: `ExecutionGraph` — lineáris, névvel ellátott kimenet-kulcsokkal (`component@version.function(inputKey) -> outputKey`). Visszafelé kompatibilis régi formátummal.
        *   [x] **Feladat**: Schema validáció bekötése `Setx`-be — `moduleDesc.Accepts` lista alapján `"$schema"` kulcs ellenőrzése. `ErrInvalid` ha nem egyezik.
        *   [x] **Feladat**: `ProofTrace` típus bevezetése — `workflow_id`, per-step `input_hash`+`output_hash` (canonical JSON SHA256), `chain_hash`, injektált `timestamp`. Determinisztikus, oldalhatás-mentes.
        *   [x] **DoD**: `make test` zöld, `make verify` zöld.

### M3 — Privát PKI verifikáció
*   **Feladatok**: saját trust store (offline root → intermediate → rövid élettartamú code signing leaf). Ellenőrzés: checksum+signature + chain verify. EKU=Code Signing, SPKI pin az intermediate-re, revocation (CRL/OCSP vagy rövid lejárat).
    *   **DoD**: rossz issuer/EKU/lejárt/visszavont → fail; jó lánc → átmegy. Auditor-barát log: signer ujjlenyomat, chain hash, végső checksum.
    *   **Architekturális megjegyzés**: A PKI működéséhez szükséges titkok (pl. privát kulcsok) betöltése nem a fájlrendszerből, hanem egy dedikált, külső titokkezelőből (pl. Vault) történik. Lásd **M8.3**.
* **Részfeladatok (checklist):**
    *    ** M3.1 — Teszt tanúsítványok kezelése
        *   [x] **Feladat**: Egy `bootstrap_testdata.sh` script létrehozása, amely dinamikusan generálja a tesztekhez szükséges teljes PKI-infrastruktúrát (Root CA, Intermediate CA, EE tanúsítványok).
        *   [x] **DoD**: A script megbízhatóan legenerálja a `cmd/relay/testdata` struktúrát, elkerülve a manuális fájlkezelést.
        *   [x] **Feladat**: A tesztek (`_test.go` fájlok) átállítása, hogy a `go:embed` segítségével beágyazott, dinamikusan generált tesztadatokat használják, és ne függjenek a kódbázisba commitált statikus tanúsítványoktól.
        *   [x] **DoD**: A tesztek futtatása nem igényel manuális tanúsítványfájl-létrehozást, és a tesztek megbízhatóan futnak különböző környezetekben.

### M4 — LIMITS érvényesítés
* **Állapot:** Részben kész
* **Feladatok:** A rendszer robusztusságának növelése erőforrás-korlátok bevezetésével, megelőzve a DoS támadásokat és a túlzott erőforrás-felhasználást.
* **Részfeladatok (checklist):**
    *   **M4.1: JSON Input Méretkorlát**
        *   [x] **Feladat**: Implementálni egy mechanizmust, amely korlátozza a bejövő JSON payload méretét.
        *   [x] **DoD**: Túlméretes JSON input esetén a rendszer 413-as HTTP státuszkóddal válaszol. A limit konfigurálható.
    *   **M4.2: WASM Modul Méretkorlát**
        *   [x] **Feladat**: Kiterjeszteni a WASM méretellenőrzést, hogy az egy globálisan konfigurálható maximumot érvényesítsen.
        *   [x] **DoD**: Túlméretes WASM modul betöltésekor a rendszer hibát jelez. A limit konfigurálható.
    *   **M4.3: CPU/Idő Korlát**
        *   [x] **Feladat**: Bevezetni egy CPU/idő budget mechanizmust a WASM futtatási műveletekhez.
        *   [x] **DoD**: `ErrTimeout` sentinel + `IsTimeout` predikátum. cicwasm TIMEOUT kód → `ErrTimeout` wrapping a `Setx`-ben. HTTP 504 a handler szinten.
    *   **M4.4: Limitértékek Konfigurálhatósága**
        *   [x] **Feladat**: Biztosítani, hogy a bevezetett limitek (JSON méret, WASM méret) konfigurálhatók legyenek.
        *   [ ] **DoD**: Minden limit könnyen módosítható a központi, séma-vezérelt konfigurációban.

### M5 — Hibamodel egységesítése
* **Állapot:** Részben kész
* **Feladatok:** A rendszer hibakezelésének sztenderdizálása és konzisztenssé tétele.
* **Részfeladatok (checklist):**
    *   **M5.1: Sentinel hibák namespacing**
        *   [x] **Feladat**: A hibák egy jól definiált névtérbe kerülnek (pl. `cabinet.ErrInvalidInput`).
        *   [x] **DoD**: A rendszerben használt összes sentinel hiba konzisztensen névtérbe van rendezve.
    *   **M5.2: Egységes JSON-payload**
        *   [x] **Feladat**: A rendszer által visszaadott hibáknak egységes JSON struktúrával kell rendelkezniük.
        *   [x] **DoD**: `writeError` helper — minden hiba `{"error":"...","code":"..."}` formátumban, Content-Type: application/json.
    *   **M5.3: HTTP status mapping**
        *   [x] **Feladat**: A különböző típusú hibákhoz megfelelő HTTP státuszkódokat kell rendelni.
        *   [x] **DoD**: A hibákhoz rendelt HTTP státuszkódok konzisztensek és a HTTP specifikációnak megfelelnek.
    *   **M5.4: Általános hibakezelési elvek**
        *   [x] **Feladat**: Biztosítani, hogy az egységes hibakezelési modell teljesüljön a teljes rendszerben.
        *   [x] **DoD**:
              *   Minden útvonal azonos szerkezetű hibát ad vissza.
              *   `errors.Is/As` működik a hibák ellenőrzésére.
              *   A logokban egyértelmű, gépileg olvasható hibakódok jelennek meg.

### M6 — Build & Release Chain
* **Állapot:** Részben kész
* **Feladatok:** 
* **Megjegyzés:** A determinisztikus build, MANIFEST, aláírások és verify pipeline alapjai megvannak, de a teljes build-signing integráció és az automatizált verify futás minden artifactre még hiányzik.
* **Részfeladatok (checklist):**
    *   **M6.1: Strukturált logolás**
        *   [x] **Feladat**: Implementálni a strukturált logolást, beleértve a `req-id`, `path`, `kind`, `méret`, `latency`, `outcome`, `checksum` mezőket.
        *   [x] **DoD**: A rendszer minden releváns eseményről konzisztens, strukturált logokat generál, amelyek tartalmazzák a megadott mezőket.
    *   **M6.2: Audit trail**
        *   [x] **Feladat**: Létrehozni egy audit trail mechanizmust, amely rögzíti a betöltött modulok aláíróját, a lánc ujjlenyomatát és a végső hash-t.
        *   [x] **DoD**: Az audit trail adatok alapján incidens esetén 1 percen belül rekonstruálható, hogy mit töltöttek be, ki írta alá, miért ment át vagy bukott.
    *   **M6.3: `/healthz` és `/readyz` endpointok**
        *   [x] **Feladat**: Implementálni a `/healthz` és `/readyz` HTTP endpointokat, amelyek ellenőrzik a rendszer állapotát (trust store, policy, storage).
        *   [x] **DoD**: A `/healthz` és `/readyz` endpointok pontosan tükrözik a rendszer működőképességét és függőségeinek állapotát.
    *   **M6.4: API Handler/Integrációs Tesztek**
        *   [x] **Feladat**: A `/set`, `/healthz`, `/readyz` és `/` HTTP handlerek tesztelése a `net/http/httptest` csomag és mockolt service-ek segítségével.
        *   [x] **DoD**: A handler tesztek lefedik a sikeres eseteket és a várt hibakódokat (4xx, 5xx). A tesztek a `make test` részei, gyorsak és nem igényelnek külső függőséget.
    *   **M6.5: End-to-End (E2E) Tesztek**
        *   [x] **Feladat**: Létrehozni egy kezdeti API tesztkészletet curl scriptek formájában, amely futó relay instance ellen fut.
        *   [x] **DoD**: `api_tests/smoke.sh` — 5 assertion, `RELAY_URL` konfigurálható, `make test-api` futtatja. Dokumentálva `ai/SYSTEM_CONTEXT.md`-ben.

### M7 — Supply-chain Validation / Artifact Trust
* **Állapot:** Részben kész
* **Feladatok:** A cél a CI/CD pipeline megbízhatóságának és a kódminőség biztosítása automatizált ellenőrzésekkel.
* **Megjegyzés:** A betöltéskori signature+hash ellenőrzés alapjai (revocation, SPKI-pinning) megvannak, de a logika nincs bekötve a futási útvonalba, és nincs rollout-gate vagy rollback-logika.
* **Részfeladatok (checklist):**
    *   **M7.1: Symbols szinkronizáció**
        *   [x] **Feladat**: Implementálni egy CI ellenőrzést, amely biztosítja, hogy a `context/SYMBOLS.md` fájl szinkronban van a kódbázis publikus szimbólumaival (drift=0).
        *   [x] **DoD**: A CI/CD pipeline sikertelen, ha a `SYMBOLS.md` tartalma eltér a kódbázisban definiált publikus szimbólumoktól.
        *   **Részfeladatok (checklist):**
            *   [x] **M7.1.1**: `docs/engineering/symbols_spec.md` létezik.
            *   [x] **M7.1.2**: `context/SYMBOLS.ALLOWPKGS` létezik.
            *   [x] **M7.1.3**: `tools/symbolsgen` implementálva.
            *   [x] **M7.1.4**: `verify.yml` "Check for symbol drift" lépés (`go run ./tools/symbolsgen && git diff --exit-code`).
            *   [x] **M7.1.5**: `context/SYMBOLS.md` baseline generálva, tetején "auto-generated" megjegyzés.
    *   **M7.2: Code quality checks (lint/staticcheck/go vet)**
        *   [ ] **Feladat**: Integrálni a lint, staticcheck és go vet eszközöket a CI/CD pipeline-ba a kódminőség automatikus ellenőrzésére.
        *   [ ] **DoD**: A CI/CD pipeline sikertelen, ha a kód nem felel meg a lintelési, statikus analízis és go vet ellenőrzéseknek.
        *   **Részfeladatok (checklist):**
            *   [ ] **M7.2.1: CI lépések hozzáadása**: Módosítsa a fő CI workflow fájlt (pl. `verify.yml`), hogy tartalmazzon dedikált lépéseket a `lint`, `staticcheck` és `go vet` futtatására a teljes kódbázison.
            *   [ ] **M7.2.2: Hiba esetén leállítás**: Konfigurálja ezeket a CI lépéseket úgy, hogy hiba esetén (pl. ha a lint/staticcheck/go vet hibát talál) a CI pipeline sikertelen legyen.
            *   [ ] **M7.2.3: Konfiguráció**: Biztosítsa, hogy az eszközök konfigurációja (ha van ilyen) a projekt gyökerében legyen, és a CI környezetben is elérhető legyen.
            *   [ ] **M7.2.4: Függőségek kezelése**: Győződjön meg róla, hogy a CI környezetben telepítve vannak a szükséges eszközök (pl. `staticcheck`).
    *   **M7.3: Per-package coverage thresholds**
        *   [ ] **Feladat**: Bevezetni per-package alapú kódlefedettségi küszöböket a CI/CD pipeline-ba.
        *   [ ] **DoD**: A CI/CD pipeline sikertelen, ha bármely package kódlefedettsége a meghatározott küszöb alá esik.
    *   **M7.4: Coverage report HTML artifact**
        *   [ ] **Feladat**: Konfigurálni a CI/CD pipeline-t, hogy generáljon HTML kódlefedettségi riportot artifactként.
        *   [ ] **DoD**: A CI/CD pipeline minden futtatás után generál egy hozzáférhető HTML kódlefedettségi riportot.

### M8 — Observability / Metrics
* **Állapot:** Részben kész
* **Feladatok:** A cél egy robusztus, biztonságos és könnyen üzemeltethető konfigurációs modell létrehozása, amely a rendszer saját képességeit használja önmaga validálására.
* **Megjegyzés:** Az alap strukturált logolás (`logrus`) használatban van, de a dedikált alap HTTP-metrikák (Prometheus) és a compile/exec időmérés hiányzik.
* **Részfeladatok (checklist):**
    *   **M8.1: Prefixált környezeti változók**
        *   [x] **Feladat**: Minden, a Relay által használt környezeti változót `CIC_` prefixszel kell ellátni a névtér-ütközések elkerülése és az egyértelmű azonosíthatóság érdekében.
        *   [x] **DoD**: A `loadConfig` függvény csak a `CIC_` prefixű változókat olvassa be. (VAULT_ADDR/VAULT_TOKEN a Vault SDK saját változói — nem átnevezve.)

    *   **M8.2: Séma-alapú önvalidáció ("Dogfooding")**
        *   [x] **Feladat**: A Relay saját konfigurációját egy, a rendszerben definiált sémával (`config/relay.config.schema.yaml`) validálja induláskor.
        *   [x] **DoD**: `cabinet.ValidateSchema` exportálva; `validateConfig` a main-ben; `config/relay.config.schema.yaml` séma dokumentálja az összes CIC_* változót. Hibás konfig → Fatal. Dokumentálva `ai/SYSTEM_CONTEXT.md`-ben.

    *   **M8.3: Titokkezelés szétválasztása (Single Responsibility)**
        *   [x] **Feladat**: A külső forrásból (pl. Vault) származó titkok betöltését egy dedikált, a fájlrendszer-alapú komponens-betöltéstől független függvénybe kell szervezni.
        *   [x] **DoD**: `loadSecretsFromVault(logger) *VaultSecrets` — kizárólag env var olvasás + Vault konfig aggregálása. `buildRecorderOption(logger, secrets)` — kapja a titkoket paraméterként. `loadInstalledComponents` nem tartalmaz titokkezelési logikát.

### M9 — Operations Safety (Circuit-Breaker, Rate-Limit) + Release Artifact Chain
* **Állapot:** Done
* **Feladatok:** Per-IP rate limiter (token bucket), concurrent request cap (semaphore), slow-loris timeouts; reprodukálható build (`-trimpath`, statikus bináris), SHA256 + CycloneDX SBOM.
* **DoD**: `make release && make release-verify` zöld; rate limiter + concurrent cap tesztelve; `CIC_RATE_LIMIT_RPS`, `CIC_MAX_CONCURRENT_REQUESTS` konfigurálható.
* **Részfeladatok (checklist):**
    *   [x] Per-IP token bucket rate limiter (429 Too Many Requests)
    *   [x] Concurrent request cap semaphore (503 Service Unavailable)
    *   [x] Slow-loris védelem (ReadHeaderTimeout/ReadTimeout/WriteTimeout/IdleTimeout)
    *   [x] `make release`: CGO_ENABLED=0, linux/amd64, -trimpath, -s -w → `dist/relay`
    *   [x] `dist/relay.sha256` + `make release-verify`
    *   [x] CycloneDX SBOM → `dist/sbom.json`
    *   [x] `dist/` bejegyezve `.gitignore`-ba

### M10 — Enterprise Rollout Features
* **Állapot:** Done
* **Feladatok:** request-body cap, slow-loris védelem, párhuzamossági limit. (M9-be olvadt — a rate limiter + slow-loris + concurrent cap ott implementálva.)
* **Részfeladatok (checklist):**
    *   [x] Request-body size cap (M4.1-M4.2 részeként)
    *   [x] Slow-loris védelem (M9-ben: ReadHeaderTimeout=5s, ReadTimeout=30s)
    *   [x] Concurrent cap (M9-ben: semaphore, CIC_MAX_CONCURRENT_REQUESTS)

### M11 — IaC Feldolgozás és Gráf Építés (Nexus IaC)
* **Állapot:** Done
* **Feladatok:** A rendszer bemenetét képező Infrastruktúra-leíró (IaC) adatok beolvasása, validálása és egy memóriabeli gráf-modellé alakítása, függetlenül attól, hogy az adat fájlrendszerből vagy API-n keresztül érkezik.
* **DoD**: A rendszer képes mind fájlrendszerből, mind (mockolt) API válaszból felépíteni ugyanazt a gráfot. A forrás típusa konfigurálható.
* **Részfeladatok (checklist):**
    *   [x] **M11.1: IaC Source Abstraction**: `IaCSource` interfész definiálása (pl. `FetchConfig`).
    *   [x] **M11.2: Implementációk**: `FileSource` (rekurzív fájl olvasás) és `UpstreamSource` (API kliens) vázának elkészítése.
    *   [x] **M11.3: Gráf Építő**: A nyers adatokból a `Relay`-`Host`-`Service` csomópontok és élek memóriabeli felépítése.
    *   [x] **M11.4: Validáció és Desired State**: A gráf konzisztenciájának ellenőrzése és transzformálása `ExpectedState` objektummá.

### M12 — Git-alapú Állapotrögzítés (Nexus Recorder)
* **Állapot:** Kész
* **Feladatok:** A Relay belső állapotának (expected/actual) perzisztens, visszakereshető rögzítése egy lokális Git repóba a hibakeresés támogatására.
* **DoD**: A Relay futása során létrejön a lokális Git repo, és minden tranzakció után megjelenik egy új commit a megfelelő branch-en, ami tartalmazza az állapot-fájlokat.
* **Részfeladatok (checklist):**
    *   [x] **M12.1: Közös Típusrendszer (`nexus/types`)**: `Host`, `Relay`, `Module`, `State` struktúrák.
    *   [x] **M12.2: Recorder Interfész**: `StateRecorder` interfész.
    *   [x] **M12.3: Git Library Integráció**: git wrapper, `InitRepo`.
    *   [x] **M12.4: Perzisztencia és Commit**: JSON szerializáció és commit logika.
    *   [x] **M12.5: WorkflowRecorder**: `Setx` → `recorder` összekötés `ExecutionRecorder` interfészen keresztül. Vault hiba nem fatális — WARN + folytatás.

### M13 — Vault Integráció és Aláírás (Nexus Crypto)
* **Állapot:** Részben kész
* **Feladatok:** A rögzített állapotok hitelességének biztosítása kriptográfiai aláírással.
* **DoD**: A Git commitok metaadatai között (pl. commit message footer vagy külön meta fájl) megjelenik a Vault által generált aláírás. A rendszer induláskor ellenőrzi a Vault kapcsolatot.
* **Részfeladatok (checklist):**
    *   [x] **M13.1: Vault Kliens**: Wrapper, HealthCheck implementálása.
    *   [x] **M13.2: Aláírás (Sign)**: Transit engine integráció.
    *   [x] **M13.3: CryptoService Interfész**: Magas szintű API definiálása.
    *   [x] **M13.4: Recorder Integráció**: Commit aláírás bekötése.
    *   [x] **M13.5: Dev Vault Bootstrap**: `make dev-vault` — lokális in-memory Vault, paraméter nélkül, Transit kulcs auto-generálva. `make dev-vault-stop` leállítás. Dokumentálva `ai/SYSTEM_CONTEXT.md`-ben.
    *   [x] **M13.6: Produkciós Vault bekötés**: `WithRecorder` functional option a `cabinet` csomagban. `buildRecorderOption` a `main.go`-ban: env változókból (`VAULT_TOKEN`, `CIC_VAULT_MOUNT`, `CIC_VAULT_KEY`, `CIC_AUDIT_REPO_PATH`) `VaultCryptoService`+`GitStateRecorder`+`WorkflowRecorder` összerakva. VAULT_TOKEN hiánya non-fatal (WARN + nil recorder).

### M14 — Központi Audit Szinkronizáció (Nexus Sync)
* **Állapot:** Done
* **Feladatok:** A lokális, részletes hibakeresési adatok tömörítése és biztonságos továbbítása egy központi audit repóba.
* **DoD**: A központi Git repóban megjelennek a napi összesített commitok. A lokális repóban a régi branchek törlődnek (prune).
* **Részfeladatok (checklist):**
    *   [x] **M14.1: Squash Logika**: Napi tömörítés implementálása.
    *   [x] **M14.2: Push Logika**: Központi repóba küldés implementálása.
    *   [x] **M14.3: Prune**: Takarítási logika implementálása.
    *   [x] **M14.4: Integrációs tesztek**: 2 ephemeral git repo (t.TempDir()), squash → push → prune round-trip; coverage ≥ 70% (83.3% elért).

### M15 — Többrétegű Biztonsági Szeparáció (Nexus Isolation)
* **Állapot:** Done
* **Feladatok:** A "Zero Trust" elv érvényesítése a titkosítási folyamatban a jogosultságok szétválasztásával.
* **DoD**: A rendszer három különálló identitást használ a műveletekhez. A plaintext adat soha nem jelenik meg a koordinátor processz memóriájában, csak a végponti izolált környezetben.
* **Részfeladatok (checklist):**
    *   [x] **M15.1: Identitás Definíciók**: Host/Relay/Module identity struktúrák.
    *   [x] **M15.2: Koordinátor**: Folyamatvezérlés implementálása.
    *   [x] **M15.3: IPC**: Kommunikációs csatorna implementálása. `IPCRequest/IPCResponse` newline-delimited JSON; `RunWorkerLoop` (echo mode nil worker-rel).
    *   [x] **M15.4: Workerek**: `SubprocessWorker` — `os.Executable()` + `--mode=module-worker`; `NewSubprocessWorker()` + `newSubprocessWorkerWithExe()` (test helper). Round-trip integrációs teszt: test binary echo mode-ban.
    *   [x] **M15.5: main.go bekötés**: `--mode=module-worker` flag → `RunWorkerLoop` indítás, HTTP listener nincs.

### M16 — GitOps-vezérelt Életciklus (Nexus Operator)
* **Állapot:** Done
* **Feladatok:** A rendszer entitásainak (hostok, modulok) dinamikus, automatizált kezelése egy központi "Infrastruktúra Állapot Repó" alapján.
* **DoD**: Egy új YAML fájl commitolása az infra repóba automatikusan eredményez egy működő, hitelesített új hostot/modult a rendszerben, emberi beavatkozás nélkül.
* **Részfeladatok (checklist):**
    *   [x] **M16.1: Watcher**: Infra repo figyelés implementálása.
    *   [x] **M16.2: Bootstrap**: HMAC-SHA256 token generálás, 24h TTL, recorder audit. (`operator/bootstrap.go`)
    *   [x] **M16.3: Lifecycle**: OnboardHost + OffboardHost, structured logrus audit. (`operator/lifecycle.go`)
    *   [x] **M16.4: main.go wiring**: `CIC_INFRA_REPO_PATH` → `PollingWatcher(30s)` + `OperatorWithLifecycle` goroutine. `CIC_OPERATOR_SECRET` hiánya Fatal ha repo be van állítva. Backward-compatible (WARN ha nincs `CIC_INFRA_REPO_PATH`).

---

## Jelenlegi állapot (STATE)

| Milestone | State       | Megjegyzés |
|-----------|-------------|------------|
| M-Konfig  | **Done**    | Séma-vezérelt önvalidáció kész (M8.1 + M8.2) |
| M0        | Done        | |
| M1        | Done        | |
| M2        | **Done**    | ExecutionGraph, schema validáció, ProofTrace kész |
| M3        | **Done**    | PKI verify + bootstrap.go wiring + TrustStoreLoaded flag + 3 PKI teszt kész |
| M4        | **Done**    | M4.4 kész: CIC_WASM_EXEC_TIMEOUT_SECONDS + range validáció |
| M5        | **Done**    | M5.2 uniform JSON error kész, minden M5 subtask zárva |
| M6        | **Done**    | M6.5 E2E smoke tests kész, minden M6 subtask zárva |
| M7        | **Done**    | M7.1-M7.4 kész: symbols drift, vet/staticcheck, per-pkg thresholds, HTML coverage |
| M8        | **Done**    | M8.1+M8.2+M8.3 kész — env prefix, dogfooding, secrets separation |
| M9        | **Done**    | `make release`: statikus bináris + sha256 + CycloneDX SBOM; `make release-verify` |
| M10       | **Done**    | Rate limiter (per-IP), concurrent cap, slow-loris timeouts implementálva |
| M11       | **Done**    | UpstreamSource kész: HTTP GET, JSON/YAML auto-detect, retry/backoff |
| M12       | **Done**    | WorkflowRecorder + Setx összekötés kész |
| M13       | **Done**    | Dev Vault bootstrap + prod recorder wiring kész |
| M14       | **Done**    | sync integrációs tesztek megvannak, coverage 83.3% (≥70%) |
| M15       | **Done**    | SubprocessWorker IPC + RunWorkerLoop + round-trip teszt; --mode=module-worker flag |
| M16       | **Done**    | M16.1-M16.3 kész + M16.4: Operator bekötve main.go-ba (CIC_INFRA_REPO_PATH) |
| M17       | **Done**    | ProofTrace.SourceDigest + computeChainHash(sourceDigest, steps); CommitHash injektálva setHandler-ből |
| M18       | **Done**    | PollingWatcher scanDir + emitDiff; CREATE/UPDATE/DELETE; 4 teszt |
| M19       | **Done**    | ci.build@1.0 native Go modul; make exec, artifact SHA-256; config/schemas/ci.build.schema.yaml |
| M20       | **Done**    | SourceDigest upgrade: pkg/sourcedigest determinisztikus tree hash; tools/sourcehash CLI; SourceTreeHash ldflags |
| M21       | **Done**    | Hermetic build boundary: docker-compose.yaml image digest-pinning; CI Go 1.24.6 exact + staticcheck@v0.6.1 |

### M17 — Source-Pinned ProofTrace (Chain Hash Integritás)
* **Állapot:** Done
* **Fő cél:** A `ProofTrace` `chain_hash` első eleme legyen a forrás hash (git HEAD commit hash vagy explicit `source_digest`). Ez biztosítja, hogy a hash-lánc a forráskódtól az output-ig folytonos — a CI use-case alappillére.
* **Részfeladatok (checklist):**
    *   [x] **M17.1**: `ProofTrace.SourceDigest string` mező (json:"source_digest,omitempty").
    *   [x] **M17.2**: `computeChainHash(sourceDigest, steps)`: source_digest az első összetevő.
    *   [x] **M17.3**: `SetPayload.SourceDigest` opcionális mező — caller injektálja.
    *   [x] **M17.4**: `setHandler`: `CommitHash` build változó injektálva SourceDigest-ként ha üres.
    *   [x] **M17.5**: `make test` + `make verify` zöld.

### M18 — PollingWatcher State Diff
* **Állapot:** Done
* **Fő cél:** A `PollingWatcher` jelenleg csak skeleton — nem detektál változást. Implementálandó a state tracking diff (CREATE/UPDATE/DELETE események) a GitOps Operator production-ready működéséhez.
* **Részfeladatok (checklist):**
    *   [x] **M18.1**: `scanDir`: path → SHA-256 content hash snapshot.
    *   [x] **M18.2**: `emitDiff`: prev vs. curr → CREATE/UPDATE/DELETE events.
    *   [x] **M18.3**: Első tick (prev=nil): minden fájl CREATE eseményt kap.
    *   [x] **M18.4**: 4 teszt: Create/Update/Delete/EmptyDir.

### M19 — CI Build Modul (Relay-on-Relay CI)
* **Állapot:** Done
* **Fő cél:** Első native Go modul a CI use-case-hez: build spec schema, Docker exec wrapper, artifact digest → ProofTrace bekötés.
* **Részfeladatok (checklist):**
    *   [x] **M19.1**: `config/schemas/ci.build.schema.yaml` — build_target, make_target, artifact_output.
    *   [x] **M19.2**: `core/modules/cibuild/cibuild.go` — make exec, 5 perc timeout, stdout/stderr logrus.
    *   [x] **M19.3**: Artifact SHA-256 digest visszaadva Output struct-ban mint step output.
    *   [x] **M19.4**: Tesztek: parseInput, fileDigest, MakeFails, Success (tmpdir Makefile).

---

## Sprint 8 — Folyamatban

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p36  | M20       | Done    | SourceDigest upgrade: source tree hash (pkg/sourcedigest + tools/sourcehash + SourceTreeHash ldflags) |
| p37  | M21       | Done    | Builder image digest-pinning: docker-compose.yaml + CI Go verzió + staticcheck pinelve |

## Sprint 7 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p33  | M9        | Done    | Timestamp kivétele release ldflags-ből — RELEASE_LD_FLAGS |
| p34  | M9        | Done    | cyclonedx-gomod @latest → pinelt verzió |
| p35  | M2        | Done    | registerNativeComponents() behívása main() runtime útvonalán |

## Sprint 6 — Lezárva

| Task | Milestone | Állapot | Leírás |
|------|-----------|---------|--------|
| p29  | M17       | Done    | ProofTrace.SourceDigest + computeChainHash anchoring |
| p30  | M18       | Done    | PollingWatcher scanDir + emitDiff (CREATE/UPDATE/DELETE) |
| p31  | M19       | Done    | ci.build@1.0 native Go modul + schema |
| p32  | M3        | Done    | PKI WrongChain e2e teszt (in-process ECDSA cert gen) |

### M20 — Source Tree Hash (Build-Input Digest Upgrade)
* **Állapot:** Done
* **Fő cél:** A `SourceDigest` default értéke CommitHash volt — ez csak a commit objektumot azonosítja, nem a teljes build-input határt. A valódi ProofTrace source-anchoring megköveteli a teljes source tree determinisztikus hash-ét.
* **Részfeladatok (checklist):**
    *   [x] **M20.1**: `pkg/sourcedigest/sourcedigest.go` — determinisztikus tree hash: WalkDir sorted, platform-independent forward-slash normalizálás, SHA-256 aggregáció. DefaultExcludes: .git, output, dist, tmp, CurrentTest.
    *   [x] **M20.2**: `tools/sourcehash/main.go` — CLI wrapper, `go run ./tools/sourcehash .` build-time futtatható.
    *   [x] **M20.3**: `cmd/relay/main.go` — `SourceTreeHash` ldflags var; setHandler: ha `SourceTreeHash != "unknown"` → tree hash, fallback CommitHash.
    *   [x] **M20.4**: `Makefile`: `release` + `build` target inline `SOURCE_HASH=$$(go run ./tools/sourcehash .)` + `-X main.SourceTreeHash=$$SOURCE_HASH`; `sourcehash` target.
    *   [x] **M20.5**: 7 unit teszt: Determinism, ContentSensitivity, AddedFile, PathSensitivity, ExcludesDir, EmptyDir, SymlinksSkipped.

### M21 — Hermetic Build Boundary
* **Állapot:** Done
* **Fő cél:** A builder environment teljes lezárása: Docker image-ek digest-pinelése, CI Go toolchain exact verzió, CI tool verziók rögzítése. Az M20 source tree hash után ez a második réteg — a build environment maga is determinisztikus.
* **Részfeladatok (checklist):**
    *   [x] **M21.1**: `docker-compose.yaml` — minden image `tag@sha256:digest` formátumra frissítve (linux/amd64 manifest digest). Re-pin parancs dokumentálva kommentben.
    *   [x] **M21.2**: `.github/workflows/verify.yml` — Go verzió `"1.24.x"` → `"1.24.6"` (mindkét job).
    *   [x] **M21.3**: `.github/workflows/verify.yml` — `staticcheck@latest` → `staticcheck@v0.6.1` (megegyezik a `cache-populate` Makefile target verziójával).
    *   [x] **M21.4**: `ai/SYSTEM_CONTEXT.md` — "Build-input boundary" tábla + digests dokumentálva + re-pin parancs.

---

## Sync-megjegyzés

Ezt a fájlt az AI támogatott beszélgetések frissítik.
A hivatalos, részletes forrás: `docs/hu/roadmap/relay_milestones.md`.
