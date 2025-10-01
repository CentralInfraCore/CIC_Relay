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
*   **Állapot:** Részben kész
*   **Feladatok:** Schema/Workflow betöltés és a CabinetService Put*/Get* útvonalak megvalósítása.
*   **Megjegyzés:** A futtató runtime nincs bekötve (WASM exec path hiányzik). A `core/cabinet/service.go`-ban a `TODO` komment egyértelműen jelzi a hiányosságot.
* **Részfeladatok (checklist):**
        *   [x] **Feladat**: A `wasmDigest` (sha256) és `wasmSize` mezők kötelezővé tétele a modul YAML-definíciójában.
        *   [x] **DoD**: A `ModuleFile` struktúra tartalmazza a kötelező mezőket, a hiányzó mezők hibát okoznak a feldolgozás során.
        *   [x] **Feladat**: A `loadModule` függvény kiegészítése a `wasmSize` ellenőrzésével.
        *   [x] **DoD**: A `loadModule` betöltéskor ellenőrzi, hogy a WASM fájl mérete megegyezik-e a YAML-ban megadott `wasmSize` értékkel. Eltérés esetén a betöltés hibával megszakad.
        *   [x] **Feladat**: A `loadModule` függvény kiegészítése a `wasmDigest` ellenőrzésével.
        *   [x] **DoD**: A `loadModule` betöltéskor ellenőrzi, hogy a WASM fájl tartalmának hash-e megegyezik-e a YAML-ban megadott `wasmDigest` értékkel. Eltérés esetén a betöltés hibával megszakad.
        *   [x] **Feladat**: A kapcsolódó tesztek frissítése és létrehozása.
        *   [x] **DoD**: A `bootstrap_test.go` tartalmaz dedikált teszteseteket a méret- és digest-eltérésekre, és azok sikeresen lefutnak.
        *   [ ] **Feladat**: A 'wasm' modulok példányosítása

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
        *   [ ] **Feladat**: Bevezetni egy CPU/idő budget mechanizmust a kanonizálási, hash számítási és WASM futtatási műveletekhez.
        *   [ ] **DoD**: Ha egy művelet túllépi a megadott időkeretet, a művelet megszakad, és a rendszer megfelelő hibával válaszol.
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
        *   [ ] **Feladat**: A rendszer által visszaadott hibáknak egységes JSON struktúrával kell rendelkezniük.
        *   [ ] **DoD**: Minden API válaszban azonos JSON hibaformátumot használunk.
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
        *   [ ] **Feladat**: Létrehozni egy kezdeti API tesztkészletet (pl. Postman collection vagy `curl` scriptek formájában), amely egy `docker compose up`-pal elindított, teljes rendszer ellen fut.
        *   [ ] **DoD**: A tesztek dokumentálva vannak, és sikeresen lefedik a fő felhasználói útvonalakat. A tesztkészlet a projekt része, és a CI pipeline egy külön (opcionális) fázisában futtatható.

### M7 — Supply-chain Validation / Artifact Trust
* **Állapot:** Részben kész
* **Feladatok:** A cél a CI/CD pipeline megbízhatóságának és a kódminőség biztosítása automatizált ellenőrzésekkel.
* **Megjegyzés:** A betöltéskori signature+hash ellenőrzés alapjai (revocation, SPKI-pinning) megvannak, de a logika nincs bekötve a futási útvonalba, és nincs rollout-gate vagy rollback-logika.
* **Részfeladatok (checklist):**
    *   **M7.1: Symbols szinkronizáció**
        *   [ ] **Feladat**: Implementálni egy CI ellenőrzést, amely biztosítja, hogy a `context/SYMBOLS.md` fájl szinkronban van a kódbázis publikus szimbólumaival (drift=0).
        *   [ ] **DoD**: A CI/CD pipeline sikertelen, ha a `SYMBOLS.md` tartalma eltér a kódbázisban definiált publikus szimbólumoktól.
        *   **Részfeladatok (checklist):**
            *   [ ] **M7.1.1: Specifikáció és szabályok definiálása**: Létrehozni a `docs/engineering/symbols_spec.md` fájlt, amely leírja a publikus API definícióját, a szimbólumok kanonikus formátumát és a szűrési szabályokat.
            *   [ ] **M7.1.2: Konfigurációs fájlok létrehozása**: Létrehozni a `context/SYMBOLS.ALLOWPKGS` fájlt a publikus csomagok listájával.
            *   [ ] **M7.1.3: Generátor eszköz implementálása**: Létrehozni a `tools/symbolsgen` eszközt, amely a specifikáció alapján generálja a `SYMBOLS.md` fájlt.
            *   [ ] **M7.1.4: CI-integráció**: Beépíteni a `verify.yml`-be egy lépést, ami a generátorral ellenőrzi a `SYMBOLS.md` naprakészségét.
            *   [ ] **M7.1.5: Baseline `SYMBOLS.md` létrehozása**: Generálni és manuálisan ellenőrizni az első, "golden" `SYMBOLS.md` fájlt.
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
        *   [ ] **Feladat**: Minden, a Relay által használt környezeti változót `CIC_` prefixszel kell ellátni a névtér-ütközések elkerülése és az egyértelmű azonosíthatóság érdekében.
        *   [ ] **DoD**: A `loadConfig` függvény csak a `CIC_` prefixű változókat olvassa be. A kód sehol nem hivatkozik prefix nélküli változókra.

    *   **M8.2: Séma-alapú önvalidáció ("Dogfooding")**
        *   [ ] **Feladat**: A Relay saját konfigurációját egy, a rendszerben definiált sémával (`relay.config.schema.yaml`) validálja induláskor.
        *   [ ] **DoD**: A Relay indításkor betölti a `relay.config.schema.yaml`-t, és validálja a saját, környezeti változókból és/vagy fájlból betöltött konfigurációját. Hibás vagy hiányos konfiguráció esetén a program egyértelmű hibaüzenettel, azonnal leáll (fail-fast). A séma a konfiguráció ön-dokumentációjaként is szolgál.

    *   **M8.3: Titokkezelés szétválasztása (Single Responsibility)**
        *   [ ] **Feladat**: A külső forrásból (pl. Vault) származó titkok betöltését egy dedikált, a fájlrendszer-alapú komponens-betöltéstől független függvénybe kell szervezni.
        *   [ ] **DoD**: Létrejön egy dedikált `loadSecretsFromVault` (vagy hasonló nevű) függvény, amely kizárólag a külső titokkezelővel való kommunikációért felel. A `loadInstalledComponents` függvény nem tartalmaz semmilyen titokkezelési vagy hálózati logikát.

### M9 — Operations Safety (Circuit-Breaker, Rate-Limit)
* **Állapot:** Hiányzik
* **Feladatok:** reprodukálható build (`-trimpath`, statikus bináris), SBOM generálás, artefaktok: bináris+hash+aláírás. README.md frissítve (cél, futtatás, config, limits, hibatérkép, health/ready). Operator guide (trust store rotáció, kulcsforgatás).
* **DoD**: új üzemeltető 15 perc alatt beállítja, artefaktok auditálhatók.
* **Megjegyzés:** Semmilyen Circuit-Breaker vagy Rate-Limiter modul, se limiter nincs a kódban.

### M10 — Enterprise Rollout Features
* **Állapot:** Hiányzik
* **Feladatok:** request-body cap + early discard, slow-loris védelem (idle timeout), párhuzamossági limit sapka a determinisztika megtartásával.
* **DoD**: terhelési próba alatt is stabil memóriahasználat és latency.
* **Megjegyzés:** A haladó biztonsági és üzemeltetési funkciók (licenc, mTLS, multi-tenant) nincsenek implementálva.

## Mérföldkövek (prioritási sorrend, részletesen) - Frissített állapot

| Milestone | Fő cél | Tényleges állapot a kódban | Megjegyzés |
| --------- | ------- | -------------------------- | ---------- |
| M0–M1     | a teszt-refaktor és a WASM checksum/strip (wabin hook) | valóban megvan; a bizonyíték a core/cabinet/validate.go és a cmd/relay/bootstrap_test.go. | A kódvizsgálat megerősítette a roadmap "valóban megvan" és "kész" állításait. |
| M2–M3     | Schema/Workflow betöltés és a CabinetService Put*/Get* útvonalak megvalósítva; futtató runtime nincs bekötve (WASM exec path hiányzik). | Schema/Workflow betöltés és a CabinetService Put*/Get* útvonalak megvalósítva; futtató runtime nincs bekötve (WASM exec path hiányzik). | A `core/cabinet/service.go` fájlban a `TODO` komment egyértelműen jelzi a WASM futtatás hiányát. |
| M4        | Limitációk (JSON, WASM, CPU/idő budget, konfigurálhatóság) | M4.1 JSON limit → részben kész (robosztus a setHandlerben); M4.2 WASM limit/strip → kész; M4.3 CPU/idő budget → hiányzik; M4.4 konfigurálhatóság → részben kész (nincs központi config). | Az M4.1 a `setHandler`-ben robusztusan implementált, de a "részben kész" még érvényes lehet, ha más handlerekben hiányzik. Az M4.3 csak a betöltési fázisra vonatkozik, nem az általános kérésfeldolgozásra/WASM futtatásra. Az M4.4 környezeti változókból olvassa be a konfigurációt, de nincs központi konfigurációs fájl. |
| M5        | Sentinel hibák rendben, az egységes APIError JSON és a státuszkód-mapping félig kész, a “minden útvonal egyforma hibaformátumot ad” még nem igaz. | Sentinel hibák rendben, az egységes APIError JSON és a státuszkód-mapping félig kész, a “minden útvonal egyforma hibaformátumot ad” még nem igaz. | A `cmd/relay/main.go` `http.Error` használata miatt a hibaválaszok nem egységesen JSON formátumúak. |
| M6 — Build & Release chain | determinisztikus build, MANIFEST, aláírások, verify pipeline | Részben kész → a repo tartalmaz: MANIFEST.sha256, scripts/gen_manifest.sh, docs/hu/en/security/*, GitHub-Actions workflowk (verify.yml, manifest-check.yml). | A megemlített komponensek valóban jelen vannak és bizonyos mértékig működnek, de a teljes build-signing integráció és az automatizált verify futás minden artifactre még hiányzik. |
| M7 — Supply-chain validation / Artifact trust | betöltéskor signature+hash ellenőrzés, rollback LKG | Részben kész → `core/cabinet/pki_verify.go` tartalmaz revocation és SPKI-pinning logikát. | A PKI ellenőrzési logika létezik, de nincs bekötve a futási útvonalba; nincs rollout-gate vagy rollback-logika. |
| M8 — Observability / Metrics | alap HTTP-metrics, compile/exec időmérés | Részben hiányzik. Logolás (`logrus`) használatban van, de Prometheus-/metrics-kód vagy compile/exec időmérés hiányzik. | A `cmd/relay/main.go` fájlban `logrus` logger van használatban strukturált logolással. Azonban a Prometheus metrikák és a compile/exec időmérés implementációja még hiányzik. |
| M9 — Operations safety (circuit-breaker, rate-limit) | hibatűrés, DoS elleni védelem | Hiányzik → semmilyen CB/RL modul, se limiter nincs a kódban. | A kódvizsgálat nem talált semmilyen implementációt circuit breaker vagy rate limit mechanizmusokra. |
| M10 — Enterprise rollout features (licenc, mTLS, multi-tenant) | haladó biztonság és üzemeltetés | Hiányzik → a roadmap csak célként említi, nincs implementáció. | Nincs jele licenckezelés, mTLS vagy multi-tenancy implementációnak a kódban. |

---

## Jelenlegi állapot (STATE)

| Milestone | State   |
|-----------| ------- |
| M-Konfig  | Missing |
| M0        | Done    |
| M1        | Done    |
| M2        | Partial |
| M3        | Partial |
| M4        | Partial |
| M5        | Partial |
| M6        | Partial |
| M7        | Partial |
| M8        | Partial |
| M9        | Missing |
| M10       | Missing |

---

## Sync-megjegyzés

Ezt a fájlt az AI támogatott beszélgetések frissítik.
A hivatalos, részletes forrás: `docs/hu/roadmap/relay_milestones.md`.
