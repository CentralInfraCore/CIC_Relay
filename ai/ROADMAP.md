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

*   **M1.1: Méret- és CAS-alapú validáció**
    *   [x] **Feladat**: Implementálni egy mechanizmust, amely korlátozza a bejövő JSON payload és a WASM modulok méretét.
    *   [x] **DoD**: Túlméretes input esetén a rendszer hibával (pl. 413) válaszol. A limitek konfigurálhatók.
    *   [ ] **Feladat**: Tényleges CAS (Content-Addressable Storage) implementálása a modulok tárolására, a tartalom-alapú címzés kihasználására.
    *   [ ] **DoD**: A modulok tárolása a kanonikus checksum-uk alapján történik, elkerülve a duplikátumokat.

*   **M1.2: WASM Kanonizálás**
    *   [x] **Feladat**: A `stripWasmModuleFunc` függvény implementálása a `wabin` segítségével, amely eltávolítja a nem determinisztikus `CustomSection`-öket.
    *   [x] **DoD**: A `strip` logika kimenete a kanonizált bájtsorozat, amely a determinisztikus checksum-számítás alapja.

*   **M1.3: Determinisztikus Checksum Igazolása**
    *   [x] **Feladat**: `TestCanonicalWasmGolden` teszt, amely igazolja, hogy két funkcionálisan azonos, de eltérő metaadatokkal rendelkező WASM modul kanonizálás után azonos checksumot kap.
    *   [x] **DoD**: A teszt sikeres, és a CI pipeline része.

### M2 — Modul YAML + CAS szerződés

A cél a `ModuleFile` struktúra és a `loadModule` függvény módosítása a szigorú CAS (Content-Addressable Storage) szerződés kikényszerítésére.

**Részfeladatok (checklist):**

*   [x] **Feladat**: A `wasmDigest` (sha256) és `wasmSize` mezők kötelezővé tétele a modul YAML-definíciójában.
*   [x] **DoD**: A `ModuleFile` struktúra tartalmazza a kötelező mezőket, a hiányzó mezők hibát okoznak a feldolgozás során.
*   [x] **Feladat**: A `loadModule` függvény kiegészítése a `wasmSize` ellenőrzésével.
*   [x] **DoD**: A `loadModule` betöltéskor ellenőrzi, hogy a WASM fájl mérete megegyezik-e a YAML-ban megadott `wasmSize` értékkel. Eltérés esetén a betöltés hibával megszakad.
*   [x] **Feladat**: A `loadModule` függvény kiegészítése a `wasmDigest` ellenőrzésével.
*   [x] **DoD**: A `loadModule` betöltéskor ellenőrzi, hogy a WASM fájl tartalmának hash-e megegyezik-e a YAML-ban megadott `wasmDigest` értékkel. Eltérés esetén a betöltés hibával megszakad.
*   [x] **Feladat**: A kapcsolódó tesztek frissítése és létrehozása.
*   [x] **DoD**: A `bootstrap_test.go` tartalmaz dedikált teszteseteket a méret- és digest-eltérésekre, és azok sikeresen lefutnak.

### M3 — Privát PKI verifikáció

* **Feladatok**: saját trust store (offline root → intermediate → rövid élettartamú code signing leaf). Ellenőrzés: checksum+signature + chain verify. EKU=Code Signing, SPKI pin az intermediate-re, revocation (CRL/OCSP vagy rövid lejárat).
* **DoD**: rossz issuer/EKU/lejárt/visszavont → fail; jó lánc → átmegy. Auditor-barát log: signer ujjlenyomat, chain hash, végső checksum.
* **Architekturális megjegyzés**: A PKI működéséhez szükséges titkok (pl. privát kulcsok) betöltése nem a fájlrendszerből, hanem egy dedikált, külső titokkezelőből (pl. Vault) történik. Lásd **M8.3**.

### M3.1 — Teszt tanúsítványok kezelése

**Részfeladatok (checklist):**

*   [x] **Feladat**: Egy `bootstrap_testdata.sh` script létrehozása, amely dinamikusan generálja a tesztekhez szükséges teljes PKI-infrastruktúrát (Root CA, Intermediate CA, EE tanúsítványok).
*   [x] **DoD**: A script megbízhatóan legenerálja a `cmd/relay/testdata` struktúrát, elkerülve a manuális fájlkezelést.
*   [x] **Feladat**: A tesztek (`_test.go` fájlok) átállítása, hogy a `go:embed` segítségével beágyazott, dinamikusan generált tesztadatokat használják, és ne függjenek a kódbázisba commitált statikus tanúsítványoktól.
*   [x] **DoD**: A tesztek futtatása nem igényel manuális tanúsítványfájl-létrehozást, és a tesztek megbízhatóan futnak különböző környezetekben.

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
    *   [x] **Feladat**: Implementálni a `/healthz` és `/readyz` HTTP endpointokat, amelyek ellenőrzik a rendszer állapotát (trust store, policy, storage).
    *   [x] **DoD**: A `/healthz` és `/readyz` endpointok pontosan tükrözik a rendszer működőképességét és függőségeinek állapotát.

*   **M6.4: API tesztelés kidolgozása és implementálása**
    *   [ ] **Feladat**: Létrehozni egy kezdeti API tesztkészletet (pl. Postman collection vagy `curl` scriptek formájában), amely lefedi a meglévő endpointokat (`/`, `/set`, `/healthz`, `/readyz`).
    *   [ ] **DoD**: A tesztek dokumentálva vannak, és sikeresen lefutnak egy lokálisan indított relay szerver ellen. A tesztkészlet a projekt része, és könnyen futtatható.

### M7 — CI-őrségek (gates)

A cél a CI/CD pipeline megbízhatóságának és a kódminőség biztosítása automatizált ellenőrzésekkel.

**Részfeladatok (checklist):**

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
    *   [x] **Feladat**: Integrálni a lint, staticcheck és go vet eszközöket a CI/CD pipeline-ba a kódminőség automatikus ellenőrzésére.
    *   [x] **DoD**: A CI/CD pipeline sikertelen, ha a kód nem felel meg a lintelési, statikus analízis és go vet ellenőrzéseknek.
    *   **Részfeladatok (checklist):**
        *   [x] **M7.2.1: CI lépések hozzáadása**: Módosítsa a fő CI workflow fájlt (pl. `verify.yml`), hogy tartalmazzon dedikált lépéseket a `lint`, `staticcheck` és `go vet` futtatására a teljes kódbázison.
        *   [x] **M7.2.2: Hiba esetén leállítás**: Konfigurálja ezeket a CI lépéseket úgy, hogy hiba esetén (pl. ha a lint/staticcheck/go vet hibát talál) a CI pipeline sikertelen legyen.
        *   [x] **M7.2.3: Konfiguráció**: Biztosítsa, hogy az eszközök konfigurációja (ha van ilyen) a projekt gyökerében legyen, és a CI környezetben is elérhető legyen.
        *   [x] **M7.2.4: Függőségek kezelése**: Győződjön meg róla, hogy a CI környezetben telepítve vannak a szükséges eszközök (pl. `staticcheck`).

*   **M7.3: Per-package coverage thresholds**
    *   [x] **Feladat**: Bevezetni per-package alapú kódlefedettségi küszöböket a CI/CD pipeline-ba.
    *   [x] **DoD**: A CI/CD pipeline sikertelen, ha bármely package kódlefedettsége a meghatározott küszöb alá esik.

*   **M7.4: Coverage report HTML artifact**
    *   [x] **Feladat**: Konfigurálni a CI/CD pipeline-t, hogy generáljon HTML kódlefedettségi riportot artifactként.
    *   [x] **DoD**: A CI/CD pipeline minden futtatás után generál egy hozzáférhető HTML kódlefedettségi riportot.

### M8 — Konfiguráció, biztonság és önvalidáció

A cél egy robusztus, biztonságos és könnyen üzemeltethető konfigurációs modell létrehozása, amely a rendszer saját képességeit használja önmaga validálására.

**Részfeladatok (checklist):**

*   **M8.1: Prefixált környezeti változók**
    *   [ ] **Feladat**: Minden, a Relay által használt környezeti változót `CIC_` prefixszel kell ellátni a névtér-ütközések elkerülése és az egyértelmű azonosíthatóság érdekében.
    *   [ ] **DoD**: A `loadConfig` függvény csak a `CIC_` prefixű változókat olvassa be. A kód sehol nem hivatkozik prefix nélküli változókra.

*   **M8.2: Séma-alapú önvalidáció ("Dogfooding")**
    *   [ ] **Feladat**: A Relay saját konfigurációját egy, a rendszerben definiált sémával (`relay.config.schema.yaml`) validálja induláskor.
    *   [ ] **DoD**: A Relay indításkor betölti a `relay.config.schema.yaml`-t, és validálja a saját, környezeti változókból és/vagy fájlból betöltött konfigurációját. Hibás vagy hiányos konfiguráció esetén a program egyértelmű hibaüzenettel, azonnal leáll (fail-fast). A séma a konfiguráció ön-dokumentációjaként is szolgál.

*   **M8.3: Titokkezelés szétválasztása (Single Responsibility)**
    *   [ ] **Feladat**: A külső forrásból (pl. Vault) származó titkok betöltését egy dedikált, a fájlrendszer-alapú komponens-betöltéstől független függvénybe kell szervezni.
    *   [ ] **DoD**: Létrejön egy dedikált `loadSecretsFromVault` (vagy hasonló nevű) függvény, amely kizárólag a külső titokkezelővel való kommunikációért felel. A `loadInstalledComponents` függvény nem tartalmaz semmilyen titokkezelési vagy hálózati logikát.

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
| M1        | Partial |
| M2        | Done    |
| M3        | Done    |
| M4        | Done    |
| M5        | Done    |
| M6        | Partial |
| M7        | Partial |
| M8        | Missing |
| M9        | Missing |
| M10       | Missing |

---

## Sync-megjegyzés

Ezt a fájlt az AI támogatott beszélgetések frissítik.
A hivatalos, részletes forrás: `docs/roadmap/relay_milestones.md`.
