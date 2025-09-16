# Relay Roadmap (AI snapshot)

Ez a fájl az AI-támogatott roadmap állapota.
→ Részletes fejlesztői doksi: [docs/roadmap/relay\_milestones.md](../docs/roadmap/relay_milestones.md)

---

## Mérföldkövek (prioritási sorrend, részletesen)

### M0 — Tesztek refaktorálása (dinamikus checksum)

* **Feladatok**: A `bootstrap_test.go` tesztjeinek teljes körű átalakítása, hogy a beégetett, statikus checksum-értékek helyett a program saját, kanonikus checksum-számítási logikáját (`cabinet.NewSchema`, `cabinet.NewWorkflow`, `cabinet.NewModuleDescriptor`) használják a tesztadatok előállításához.
* **DoD**: A tesztek már nem tartalmaznak statikus hash-értékeket egyetlen komponens (séma, workflow, modul) esetében sem. A tesztek a `cabinet` csomag `New...` konstruktorait hívják a helyes checksum dinamikus generálásához, így a tesztek ellenállóbbak a jövőbeli változásokkal szemben. A build sikeres.

### M1 — WASM checksum „készre”

* **Feladatok**: WASM modulok kanonizálása (pl. `name`, `producers`, custom szekciók szűrése), méretlimit (pl. 16 MB), egységes `CanonWasmBytes` előállítása. CAS gyorsítás opcionálisan YAML digest/size alapján.
* **DoD**: két toolchainből készült azonos modul → azonos hash; túlméret → 413/422 hiba; golden test: inline vs. fájl-betöltött WASM hash egyezés.

### M2 — Modul YAML + CAS szerződés

* **Feladatok**: YAML header egységes a schema/workflow blokkokkal; kötelező `digest (sha256)` és `size`; opcionális `path`, `store` hintként. Betöltésnél path-normalizáció, symlink-tilalom, digest-ellenőrzés.
* **DoD**: azonos YAML+WASM → azonos hash, környezettől függetlenül. Hibás digest/size → load fail. Dokumentált „nyers vs. stripelt” szerződés.

### M3 — Privát PKI verifikáció

* **Feladatok**: saját trust store (offline root → intermediate → rövid élettartamú code signing leaf). Ellenőrzés: checksum+signature + chain verify. EKU=Code Signing, SPKI pin az intermediate-re, revocation (CRL/OCSP vagy rövid lejárat).
* **DoD**: rossz issuer/EKU/lejárt/visszavont → fail; jó lánc → átmegy. Auditor-barát log: signer ujjlenyomat, chain hash, végső checksum.

### M4 — LIMITS érvényesítés

* **Feladatok**: JSON input cap (pl. 2–4 MB), WASM cap (M1 alapján), CPU/time budget a kanonizálás/hash során.
* **DoD**: túlméret → 413/422; túl hosszú futás → megszakítás. Limitértékek konfigurálhatók, safe defaults mellett.

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
| M1        | Partial |
| M2        | Partial |
| M3        | Partial |
| M4        | Missing |
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
