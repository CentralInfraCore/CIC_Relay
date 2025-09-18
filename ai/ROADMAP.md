# Relay Roadmap (AI snapshot)

Ez a fájl az AI-támogatott roadmap állapota.
→ Részletes fejlesztői doksi: [docs/roadmap/relay_milestones.md](../docs/roadmap/relay_milestones.md)

---

## Mérföldkövek (prioritási sorrend, részletesen)

### M0 — Tesztek refaktorálása (dinamikus checksum)

* **Feladatok**: A `bootstrap_test.go` tesztjeinek teljes körű átalakítása, hogy a beégetett, statikus checksum-értékek helyett a program saját, kanonikus checksum-számítási logikáját (`cabinet.NewSchema`, `cabinet.NewWorkflow`, `cabinet.NewModuleDescriptor`) használják a tesztadatok előállításához.
* **DoD**: A tesztek már nem tartalmaznak statikus hash-értékeket egyetlen komponens (séma, workflow, modul) esetében sem. A tesztek a `cabinet` csomag `New...` konstruktorait hívják a helyes checksum dinamikus generálásához, így a tesztek ellenállóbbak a jövőbeli változásokkal szemben. A build sikeres.

### M1 — WASM checksum „készre”

A cél egy olyan kanonikus checksum-eljárás létrehozása, amely garantálja, hogy a funkcionálisan azonos WASM modulok, függetlenül a fordítási környezettől, azonos hash-t kapjanak.

**Részfeladatok (checklist):**

*   **M1.1: Méret- és CAS-alapú validáció (gyorsítótárazás)**
    *   [x] **Feladat**: Implementálni egy mechanizmust, amely korlátozza a bejövő JSON payload méretét (pl. 2-4 MB). Túlméretes modul esetén a betöltésnek hibával meg kell szakadnia.
    *   [x] **DoD**: Túlméretes JSON input esetén a rendszer 413-as vagy 422-es HTTP státuszkóddal válaszol, és a kérés feldolgozása megszakad. A limit konfigurálható.

*   **M1.2: WASM Kanonizálás (a \"lényeg\" megőrzése)**
    *   [x] **Feladat**: A `stripWasmModuleFunc` függvény implementálva a `wabin` segítségével. A logika dekódolja a modult, eltávolítja az összes `CustomSection`-t, majd újraenkódolja a binárist.
    *   [x] **DoD**: A `strip` logika kimenete a kanonizált bájtsorozat, amely a determinisztikus checksum-számítás alapja.

*   **M1.3: Definition of Done (DoD) teljesítése**
    *   [x] **Feladat**: A `TestCanonicalWasmGolden` teszt igazolja, hogy két, funkcionálisan azonos, de eltérő metaadatokkal rendelkező WASM modul kanonizálás után azonos checksumot kap.
    *   [x] **DoD**: A `github.com/tetratelabs/wabin` könyvtár hozzáadva a projekthez a stabil és publikus API-n keresztüli WASM-kezeléshez.

### M2 — Modul YAML + CAS szerződés

* **Feladatok**: A `ModuleFile` struktúra és a `loadModule` függvény módosítása a szigorú CAS (Content-Addressable Storage) szerződés kikényszerítésére.
* **DoD**: A `wasmDigest` (sha256) és `wasmSize` mezők kötelezővé téve a modul YAML-definíciójában. A `loadModule` függvény betöltéskor ellenőrzi, hogy a WASM fájl tartalma és mérete megegyezik-e a YAML-ban megadott értékekkel. A hiányzó vagy hibás értékek a modul betöltésének megszakadását okozzák. A kapcsolódó tesztek frissítve és sikeresen lefutnak.

### M3 — Privát PKI verifikáció

* **Feladatok**: saját trust store (offline root → intermediate → rövid élettartamú code signing leaf). Ellenőrzés: checksum+signature + chain verify. EKU=Code Signing, SPKI pin az intermediate-re, revocation (CRL/OCSP vagy rövid lejárat).
* **DoD**: rossz issuer/EKU/lejárt/visszavont → fail; jó lánc → átmegy. Auditor-barát log: signer ujjlenyomat, chain hash, végső checksum.

### M4 — LIMITS érvényesítés

A cél a rendszer robusztusságának növelése erőforrás-korlátok bevezetésével, megelőzve a DoS támadásokat és a túlzott erőforrás-felhasználást.

**Részfeladatok (checklist):**

*   **M4.1: JSON Input Méretkorlát**
    *   [x] **Feladat**: Implementálni egy mechanizmust, amely korlátozza a bejövő JSON payload méretét (pl. 2-4 MB).
    *   [x] **DoD**: Túlméretes JSON input esetén a rendszer 413-as vagy 422-es HTTP státuszkóddal válaszol, és a kérés feldolgozása megszakad. A limit konfigurálható.

*   **M4.2: WASM Modul Méretkorlát**
    *   [x] **Feladat**: Kiterjeszteni az M1-ben bevezetett WASM méretellenőrzést, hogy az egy globálisan konfigurálható maximumot (pl. 16 MB) érvényesítsen.
    *   [x] **DoD**: Túlméretes WASM modul betöltésekor a rendszer hibát jelez, és a modul nem kerül feldolgozásra. A limit konfigurálható.

*   **M4.3: CPU/Idő Korlát Kanonizálás/Hash Számítás Során**
    *   [x] **Feladat**: Bevezetni egy CPU/idő budget mechanizmust a kanonizálási és hash számítási műveletekhez, hogy megakadályozzuk a túl hosszú ideig tartó feldolgozást.
    *   [x] **DoD**: Ha a kanonizálás vagy hash számítás túllépi a megadott időkeretet, a művelet megszakad, és a rendszer megfelelő hibával válaszol. A budget konfigurálható.

*   **M4.4: Limitértékek Konfigurálhatósága és Alapértelmezése**
    *   [x] **Feladat**: Biztosítani, hogy az összes bevezetett limit (JSON méret, WASM méret, CPU/idő budget) konfigurálható legyen, és biztonságos alapértelmezett értékekkel rendelkezzen.
    *   [x] **DoD**: A limitek könnyen módosíthatók a konfigurációban, és a rendszer stabilan működik az alapértelmezett értékekkel is.

### M5 — Hibamodel egységesítése

A cél a rendszer hibakezelésének sztenderdizálása és konzisztenssé tétele, mind a belső logikában, mind a külső kommunikációban.

**Részfeladatok (checklist):**

*   **M5.1: Sentinel hibák namespacing**
    *   [x] **Feladat**: A hibák egy jól definiált névtérbe kerülnek (pl. `cabinet.ErrInvalidInput`), segítve a hibák azonosítását és kezelését a kódban.
    *   [x] **DoD**: A rendszerben használt összes sentinel hiba konzisztensen névtérbe van rendezve, és a kód könnyen azonosítja és kezeli őket.

*   **M5.2: Egységes JSON-payload**
    *   [x] **Feladat**: A rendszer által visszaadott hibáknak egységes JSON struktúrával kell rendelkezniük (code, message, details), amely tartalmazza a hibakódot, egy üzenetet és opcionálisan további részleteket.
    *   [x] **DoD**: Minden API válaszban azonos JSON hibaformátumot használunk, ami megkönnyíti a kliens oldali feldolgozást.
    *   **Részfeladatok (checklist):**
        *   [x] **M5.2.1: `APIError` struktúra definiálása**: Hozzon létre egy `APIError` struktúrát `Code` (string), `Message` (string) és `Details` ([]FieldError) mezőkkel.
        *   [x] **M5.2.2: Konverziós logika implementálása**: Készítsen egy függvényt vagy metódust, amely a belső hibákat (pl. `ValidationError`) átalakítja `APIError` objektummá.
        *   [x] **M5.2.3: JSON szerializáció**: Biztosítsa, hogy az `APIError` objektum JSON-né szerializálva kerüljön vissza az API válaszokban.

*   **M5.3: HTTP status mapping**
    *   [x] **Feladat**: A különböző típusú hibákhoz megfelelő HTTP státuszkódokat kell rendelni (400/409/413/422/500), hogy a kliensek a státuszkód alapján is tudjanak reagálni a hibákra.
    *   [x] **DoD**: A hibákhoz rendelt HTTP státuszkódok konzisztensek és a HTTP specifikációnak megfelelnek.

*   **M5.4: Általános hibakezelési elvek**
    *   [x] **Feladat**: Biztosítani, hogy az egységes hibakezelési modell teljesüljön a teljes rendszerben.
    *   [x] **DoD**:
        *   Minden útvonal azonos szerkezetű hibát ad vissza.
        *   `errors.Is/As` működik a hibák ellenőrzésére.
        *   A logokban egyértelmű, gépileg olvasható hibakódok jelennek meg.

### M6 — Observability & üzemeltethetőség

A cél a rendszer megfigyelhetőségének és üzemeltethetőségének javítása, biztosítva, hogy incidens esetén gyorsan rekonstruálható legyen a történtek.

**Részfeladatok (checklist):**

*   **M6.1: Strukturált logolás**
    *   [x] **Feladat**: Implementálni a strukturált logolást, beleértve a `req-id`, `path`, `kind`, `méret`, `latency`, `outcome`, `checksum` mezőket.
    *   [x] **DoD**: A rendszer minden releváns eseményről konzisztens, strukturált logokat generál, amelyek tartalmazzák a megadott mezőket.

*   **M6.2: Audit trail**
    *   [x] **Feladat**: Létrehozni egy audit trail mechanizmust, amely rögzíti a betöltött modulok aláíróját, a lánc ujjlenyomatát és a végső hash-t.
    *   [x] **DoD**: Az audit trail adatok alapján incidens esetén 1 percen belül rekonstruálható, hogy mit töltöttek be, ki írta alá, miért ment át vagy bukott.

*   **M6.3: `/healthz` és `/readyz` endpointok**
    *   [ ] **Feladat**: Implementálni a `/healthz` és `/readyz` HTTP endpointokat, amelyek ellenőrzik a rendszer állapotát (trust store, policy, storage).
    *   [ ] **DoD**: A `/healthz` és `/readyz` endpointok pontosan tükrözik a rendszer működőképességét és függőségeinek állapotát.

### M7 — CI-őrségek (gates)

A cél a CI/CD pipeline megbízhatóságának és a kódminőség biztosítása automatizált ellenőrzésekkel.

**Részfeladatok (checklist):**

*   **M7.1: Symbols synchronization**
    *   [ ] **Feladat**: Implementálni egy CI ellenőrzést, amely biztosítja, hogy a `SYMBOLS.md` fájl szinkronban van a kódbázis publikus szimbólumaival (drift=0).
    *   [ ] **DoD**: A CI/CD pipeline sikertelen, ha a `SYMBOLS.md` tartalma eltér a kódbázisban definiált publikus szimbólumoktól.

*   **M7.2: Code quality checks (lint/staticcheck/go vet)**
    *   [ ] **Feladat**: Integrálni a lint, staticcheck és go vet eszközöket a CI/CD pipeline-ba a kódminőség automatikus ellenőrzésére.
    *   [ ] **DoD**: A CI/CD pipeline sikertelen, ha a kód nem felel meg a lintelési, statikus analízis és go vet ellenőrzéseknek.
    *   **Részfeladatok (checklist):**
        *   [ ] **M7.2.1: CI lépések hozzáadása**: Módosítsa a fő CI workflow fájlt (pl. `verify.yml`), hogy tartalmazzon dedikált lépéseket a `lint`, `staticcheck` és `go vet` futtatására a teljes kódbázison.
        *   [ ] **M7.2.2: Hiba esetén leállítás**: Konfigurálja ezeket a CI lépéseket úgy, hogy hiba esetén (pl. ha a lint/staticcheck/go vet hibát talál) a CI pipeline sikertelen legyen.
        *   [ ] **M7.2.3: Konfiguráció**: Biztosítsa, hogy az eszközök konfigurációja (ha van ilyen) a projekt gyökerében legyen, és a CI környezetben is elérhető legyen.
        *   [ ] **M7.2.4: Függőségek kezelése**: Győződjön meg róla, hogy a CI környezetben telepítve vannak a szükséges eszközök (pl. `staticcheck`).

*   **M7.3: Per-package coverage thresholds**
    *   [x] **Feladat**: Bevezetni per-package alapú kódlefedettségi küszöböket a CI/CD pipeline-ba.
    *   [x] **DoD**: A CI/CD pipeline sikertelen, ha bármely package kódlefedettsége a meghatározott küszöb alá esik.

*   **M7.4: Coverage report HTML artifact**
    *   [ ] **Feladat**: Konfigurálni a CI/CD pipeline-t, hogy generáljon HTML kódlefedettségi riportot artifactként.
    *   [ ] **DoD**: A CI/CD pipeline minden futtatás után generál egy hozzáférhető HTML kódlefedettségi riportot.

### M8 — Konfiguráció & biztonság

* **Feladatok**: kötelező flag/env (trust store path, max-sizes, policy file, log level). Safe defaults: prod → deny-by-default; dev → lazább. Fail-fast hibás konfig esetén.

* **DoD**: félkonfig esetén nem indul; hibaüzenet egyértelmű, auditálható.

### M9 — Release & dokumentáció

* **Feladatok**: reprodukálható build (`-trimpath`, statikus bináris), SBOM generálás, artefaktok: bináris+hash+aláírás. README.md frissítve (cél, futtatás, config, limits, hibatérkép, health/ready). Operator guide (trust store rotáció, kulcsforgatás).
* **DoD**: új üzemeltető 15 perc alatt beállítja, artefaktok auditálhatók.

### M10 — (Opcionális) DoS/robosztusság

* **Feladatok**: request-body cap + early discard, slow-loris védelem (idle timeout), párhuzamossági limit sapka a determinisztika megtartásával.
* **DoD**: terhelési próba alatt is stabil memóriahasználat és latency.

---

## Jelenlegi állapot (STATE)

| Milestone | State   |
| --------- | ------- |
| M0        | Done    |
| M1        | Done    |
| M2        | Done    |
| M3        | Done    |
| M4        | Done    |
| M5        | Done    |
| M6        | Partial |
| M7        | Partial |
| M8        | Missing |
| M9        | Partial |
| M10       | Missing |

---

## Sync-megjegyzés

Ezt a fájlt az AI támogatott beszélgetések frissítik.
A hivatalos, részletes forrás: `docs/roadmap/relay_milestones.md`.\

