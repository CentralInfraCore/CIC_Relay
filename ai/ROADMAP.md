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
    *   [x] **Méretlimit ellenőrzés:** A WASM modul betöltésekor ellenőrizni kell, hogy a mérete nem haladja-e meg a globális maximumot (pl. 16 MB). Túlméretes modul esetén a betöltésnek hibával meg kell szakadnia. *(Kész)*
    *   [x] **CAS-gyorsítás:** A modul YAML-jában megadott `digest` (sha256) és `size` alapján ellenőrizni kell, hogy a modul már szerepel-e a belső tárolóban (Content-Addressable Store). Ha igen, a feldolgozás kihagyható. *(Kész)*

*   **M1.2: WASM Kanonizálás (a "lényeg" megőrzése)**
    *   [x] **WASM-parsoló könyvtár integrálása:** A `github.com/tetratelabs/wabin` könyvtár hozzáadva a projekthez a stabil és publikus API-n keresztüli WASM-kezeléshez. *(Kész)*
    *   [x] **Szekciók eltávolítása (`strip` logika):** A `stripWasmModuleFunc` függvény implementálva a `wabin` segítségével. A logika dekódolja a modult, eltávolítja az összes `CustomSection`-t, majd újraenkódolja a binárist. *(Kész)*
    *   [x] **`CanonWasmBytes` előállítása:** A `strip` logika kimenete a kanonizált bájtsorozat, amely a determinisztikus checksum-számítás alapja. *(Kész)*

*   **M1.3: Definition of Done (DoD) teljesítése**
    *   [x] **Golden Test implementálása:** A `TestCanonicalWasmGolden` teszt igazolja, hogy két, funkcionálisan azonos, de eltérő metaadatokkal rendelkező WASM modul kanonizálás után azonos checksumot kap. *(Kész)*

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
    *   [ ] **Feladat**: Kiterjeszteni az M1-ben bevezetett WASM méretellenőrzést, hogy az egy globálisan konfigurálható maximumot (pl. 16 MB) érvényesítsen.
    *   [ ] **DoD**: Túlméretes WASM modul betöltésekor a rendszer hibát jelez, és a modul nem kerül feldolgozásra. A limit konfigurálható.

*   **M4.3: CPU/Idő Korlát Kanonizálás/Hash Számítás Során**
    *   [ ] **Feladat**: Bevezetni egy CPU/idő budget mechanizmust a kanonizálási és hash számítási műveletekhez, hogy megakadályozzuk a túl hosszú ideig tartó feldolgozást.
    *   [ ] **DoD**: Ha a kanonizálás vagy hash számítás túllépi a megadott időkeretet, a művelet megszakad, és a rendszer megfelelő hibával válaszol. A budget konfigurálható.

*   **M4.4: Limitértékek Konfigurálhatósága és Alapértelmezése**
    *   [ ] **Feladat**: Biztosítani, hogy az összes bevezetett limit (JSON méret, WASM méret, CPU/idő budget) konfigurálható legyen, és biztonságos alapértelmezett értékekkel rendelkezzen.
    *   [ ] **DoD**: A limitek könnyen módosíthatók a konfigurációban, és a rendszer stabilan működik az alapértelmezett értékekkel is.

### M5 — Hibamodel egységesítése

* **Feladatok**: sentinel hibák namespacing (pl. `cabinet.ErrInvalidInput`), egységes JSON-payload (code, message, details), HTTP status mapping (400/409/413/422/500).
* **DoD**: minden útvonal azonos szerkezetű hibát ad vissza. `errors.Is/As` működik, logban egyértelmű hibakódok.

### M6 — Observability & üzemeltethetőség

* **Feladatok**: strukturált log (req-id, path, kind, méret, latency, outcome, checksum), audit trail (betöltött modul signer, chain fingerprint, végső hash), `/healthz` és `/readyz` endpointok (trust store, policy, storage ellenőrzés).
* **DoD**: incidens esetén 1 percen belül rekonstruálható: mit töltöttünk, ki írta alá, miért ment át vagy bukott.

### M7 — CI-őrségek (gates)

* **Feladatok**: symbols-sync (`SYMBOLS.md` drift=0), lint/staticcheck/go vet, per-csomag coverage küszöbök, coverage riport HTML artifact.
* **DoD**: API drift és quality regresszió CI-ban megfogva. Coverage stabil.

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
| M4        | Partial |
| M5        | Done    |
| M6        | Missing |
| M7        | Partial |
| M8        | Missing |
| M9        | Partial |
| M10       | Missing |

---

## Sync-megjegyzés

Ezt a fájlt az AI támogatott beszélgetések frissítik.
A hivatalos, részletes forrás: `docs/roadmap/relay_milestones.md`.
