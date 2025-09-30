# Relay Mérföldkövek: Részletes Terv

Ez a dokumentum a Relay projekt fejlesztési mérföldköveit részletezi. Míg az `ai/ROADMAP.md` egy magas szintű, AI-barát státuszkövető, ez a fájl a "miérteket" és a részletesebb technikai megfontolásokat tartalmazza emberi olvasók számára.

---

### M0 — Tesztek refaktorálása (dinamikus checksum)

*   **Cél**: A tesztkörnyezet robusztusságának növelése.
*   **Probléma**: A korábbi tesztek statikus, előre kiszámolt checksum-értékeket tartalmaztak. Ez a megközelítés rendkívül törékeny: ha a kanonizálási vagy hash-elési logika megváltozik, az összes ilyen tesztadat érvénytelenné válik, és manuálisan kell őket frissíteni.
*   **Megoldás**: A teszteket úgy alakítottuk át, hogy futásidőben, a `cabinet` csomag saját, kanonikus konstruktorait (`NewSchema`, `NewWorkflow`, `NewModuleDescriptor`) használva generálják a referencia checksum-értékeket. Így a tesztek ellenállóvá válnak a hash-elési logika változásaival szemben, és mindig a program aktuális állapotát tükrözik.
*   **Definition of Done**: A tesztek egyetlen komponens esetében sem tartalmaznak statikus hash-értékeket. A build sikeres, és a tesztek megbízhatóan futnak.

### M1 — WASM checksum „készre”

*   **Cél**: Determinisztikus, a fordítási környezettől független checksum létrehozása a WASM modulokhoz.
*   **Probléma**: A WASM binárisok tartalmazhatnak nem funkcionális, de a fájl tartalmát befolyásoló metaadatokat (pl. `CustomSection`). Emiatt két, funkcionálisan teljesen azonos modul hash-e eltérhet.
*   **Megoldás**: A `github.com/tetratelabs/wabin` könyvtár segítségével implementáltunk egy kanonizálási lépést (`stripWasmModuleFunc`), amely betöltéskor eltávolítja ezeket a nem determinisztikus szekciókat. A checksum-számítás már ezen a "csupaszított", kanonikus binárison történik.
*   **Definition of Done**: A `TestCanonicalWasmGolden` teszt igazolja, hogy a kanonizálás működik. A méretkorlátok ellenőrzése implementálva van. A tényleges CAS (Content-Addressable Storage) alapú tárolás még hiányzik, de a validációhoz szükséges alapok elkészültek.

### M2 — Modul YAML + CAS szerződés

*   **Cél**: A modul definíciója és a hozzá tartozó bináris közötti szigorú, ellenőrizhető kapcsolat megteremtése.
*   **Probléma**: A modul YAML-fájlja és a `.wasm` bináris tartalma könnyen elcsúszhat egymástól, ami integritási problémákhoz vezet.
*   **Megoldás**: A modul YAML-definíciójában kötelezővé tettük a `wasmSize` (a bináris mérete bájtban) és a `wasmDigest` (a kanonizált bináris sha256 hash-e) mezőket. A `loadModule` függvény betöltéskor szigorúan ellenőrzi, hogy a hivatkozott `.wasm` fájl mérete és hash-e pontosan megegyezik-e a YAML-ban definiált értékekkel.
*   **Definition of Done**: A `loadModule` függvény hibát dob, ha a méret vagy a digest nem egyezik. A kapcsolódó tesztek lefedik ezeket a hibás eseteket.

### M3 — Privát PKI verifikáció

*   **Cél**: A betöltött komponensek (sémák, workflow-k, modulok) digitális aláírásának ellenőrzése egy privát PKI-infrastruktúra segítségével.
*   **Megoldás**: A rendszer egy trust store-t használ (Root CA → Intermediate CA), amellyel ellenőrzi a komponensekhez csatolt aláíró tanúsítványok láncát, érvényességét, és a `codeSigning` kulcshasználat meglétét (EKU).
*   **Definition of Done**: A rendszer visszautasítja a lejárt, visszavont, vagy nem megfelelő EKU-val rendelkező tanúsítvánnyal aláírt komponenseket. A sikeres és sikertelen ellenőrzésekről audit-barát logok készülnek.

### M3.1 — Teszt tanúsítványok kezelése

*   **Cél**: A tesztekhez szükséges PKI-artefaktumok automatizált és megbízható kezelése.
*   **Probléma**: A verziókezelőbe commitált statikus tanúsítványok nehezen kezelhetők, lejáratuk problémát okoz, és biztonsági kockázatot jelentenek.
*   **Megoldás**: Létrehoztunk egy `bootstrap_testdata.sh` scriptet, amely a tesztek futtatása előtt dinamikusan generálja a teljes teszt-PKI-t. A tesztek a `go:embed` direktívát használják, hogy ezeket a frissen generált adatokat ágyazzák be, teljesen függetlenítve magukat a termelési kódtól.
*   **Definition of Done**: A `make test` parancs előfeltétele a `bootstrap_testdata.sh` sikeres lefutása. A tesztek nem tartalmaznak commitált tanúsítványokat.

### M4 — LIMITS érvényesítés

*   **Cél**: A rendszer védelme a DoS (Denial of Service) támadások és a túlzott erőforrás-felhasználás ellen.
*   **Megoldás**: Több szintű korlátozást vezettünk be: a bejövő JSON kérések méretére, a feltölthető WASM modulok méretére, valamint a CPU-intenzív hash-elési és kanonizálási műveletekre fordítható időre. Ezek a limitek környezeti változókból konfigurálhatók.
*   **Definition of Done**: A rendszer a megfelelő HTTP hibakóddal (pl. 413) válaszol, ha bármelyik limitet túllépik. A limitek biztonságos alapértelmezett értékekkel rendelkeznek.

### M5 — Hibamodel egységesítése

*   **Cél**: Konzisztens, strukturált és gépileg feldolgozható hibakezelés a teljes API-n.
*   **Megoldás**: Definiáltunk egy egységes `APIError` JSON-struktúrát (`code`, `message`, `details`). A belső, Go-specifikus hibákat (pl. `cabinet.ErrInvalidInput`) egy middleware vagy egy dedikált hibakezelő réteg alakítja át erre a standard formátumra, és rendeli hozzá a megfelelő HTTP státuszkódot (4xx/5xx).
*   **Definition of Done**: Minden API végpont azonos struktúrájú hibát ad vissza. A belső hibák `errors.Is/As` segítségével ellenőrizhetők. A logok a strukturált hibakódokat tartalmazzák.

### M6 — Observability & üzemeltethetőség

*   **Cél**: A rendszer belső állapotának és működésének megfigyelhetővé tétele.
*   **Megoldás**: Minden kéréshez egyedi `req-id`-t rendelünk, és strukturált logokat (JSON formátumban) generálunk, amelyek tartalmazzák a legfontosabb attribútumokat (pl. `latency`, `outcome`, `checksum`). Létrehoztuk a Kubernetes-környezetben elvárt `/healthz` és `/readyz` végpontokat is.
*   **Definition of Done**: Incidens esetén a logok és az audit trail alapján perceken belül rekonstruálható egy eseménysor. A health/ready probe-ok pontosan jelzik a szolgáltatás állapotát.

### M7 — CI-őrségek (gates)

*   **Cél**: A kódminőség és a projekt konzisztenciájának automatizált biztosítása.
*   **Megoldás**: A CI pipeline-t kiegészítettük `lint`, `staticcheck`, `go vet` ellenőrzésekkel, valamint csomagonkénti kódlefedettségi küszöbökkel. A pipeline hibával leáll, ha bármelyik ellenőrzés sikertelen.
*   **Definition of Done**: A CI pipeline megbízhatóan jelzi a minőségi vagy formai problémákat, megakadályozva a hibás kód bekerülését a fő ágba.

### M8 — Konfiguráció, biztonság és önvalidáció

*   **Cél**: Robusztus, biztonságos és könnyen üzemeltethető konfigurációs modell létrehozása.
*   **Megoldás**: A következő elveket vezetjük be:
    1.  **Prefixált környezeti változók**: Minden konfigurációs változót `CIC_` prefixszel látunk el a névtér-ütközések elkerülése érdekében.
    2.  **Séma-alapú önvalidáció**: A Relay induláskor a saját séma-validációs motorjával ellenőrzi a saját konfigurációját egy beépített `relay.config.schema.yaml` alapján (ún. "dogfooding").
    3.  **Titokkezelés szétválasztása**: A titkok (pl. Vault-ból) és a komponensek (fájlrendszerből) betöltése két külön, egyértelmű felelősségi körű függvénybe (`loadSecretsFromVault`, `loadInstalledComponents`) kerül.
*   **Definition of Done**: A Relay hibás vagy hiányos konfiguráció esetén el sem indul (fail-fast). A titokkezelés logikája teljesen független a komponensek betöltésétől.

### M9 — Release & dokumentáció

*   **Cél**: Reprodukálható, auditálható és könnyen telepíthető release-ek létrehozása.
*   **Megoldás**: A build folyamatnak statikus, `-trimpath` flaggel ellátott binárisokat kell generálnia. Minden release-hez tartoznia kell egy SBOM (Software Bill of Materials) dokumentumnak, valamint aláírt hash-eknek. A `README.md`-t és egy `Operator Guide`-ot is frissíteni kell a telepítési és üzemeltetési tudnivalókkal.
*   **Definition of Done**: Egy új üzemeltető a dokumentáció alapján 15 percen belül képes beállítani és elindítani a szolgáltatást. A release artefaktumok integritása ellenőrizhető.

### M10 — (Opcionális) DoS/robosztusság

*   **Cél**: A rendszer ellenállóképességének további növelése.
*   **Megoldás**: Fejlettebb DoS-védelmi technikák bevezetése, mint a `slow-loris` támadások elleni idle timeout, vagy a párhuzamos kérések számának limitálása.
*   **Definition of Done**: A rendszer terheléses tesztek alatt is stabil memóriahasználatot és válaszidőt mutat.
