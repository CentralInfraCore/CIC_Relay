# WASM iSDK — Host és Guest koncepció (v1)

## 0) Cél és hatókör

**Cél:** Egyszerű, determinisztikus, modulfejlesztő-barát vendégoldali SDK (iSDK) és a hozzá tartozó minimum host-keret, amely a YAML-alapú, irányított fa (tree-as-graph) workflow-ban elsődlegesen **szinkron** módon futtat **core** és **wasm** lépéseket.

**Hatókör:** Koncepció és szerződés-szintű leírás; nem tartalmaz megvalósítási részleteket.

---

## 1) Kontextus: YAML irányított fa + workflow

* A forrás egy **YAML formátumú, irányított fa**. Minden csomópont: `type` (`core|wasm|group`), `action`, `io` (in/out), opcionális `on_error`, `children`.
* A végrehajtást egy **workflow** intézi, amely **determinista sorrendben** (pl. pre-order) bejárja a fát.
* A **diszpécser (core függvény)** minden csomópontnál dönt: helyi feldolgozás (core/wasm hívás) **vagy** al-workflow indítás a gyerek(ek)re.
* **Nincs implicit Context**: csak a YAML leírás és **séma-validáció** irányít.

---

## 2) Szereplők és határok

* **iSDK (guest/wasm oldali SDK):** a WASM modulba linkelt könyvtár a modulfejlesztőknek. Elrejti a memóriakezelést és az ABI-t; egységes exportot biztosít.
* **Host keret (executor/diszpécser oldali minimál réteg):** a diszpécser hívásait WASM-hívássá alakítja; kezeli a modul betöltését, a szinkron hívást, a limiteket és a séma-validációt **a hívás körül**.

> A YAML **csak** azt írja le, *mit* futtatunk és *milyen IO-kötésekkel*; a *hogyan* (ABI, memóriakezelés, pool, timeout) a host keret és/vagy policy dolga.

---

## 3) Alapelvek

* **Determinista futás**: ugyanaz a YAML → ugyanaz a bejárás és eredmény.
* **Egységes hívási modell**: a diszpécser szemében a `core` és a `wasm` lépés ugyanúgy `input → output` transzformáció.
* **Séma-vezérelt IO**: belépés előtt és kilépés után sémák szerint validálunk.
* **Elsődlegesen szinkron**: a v1 célja a stabil, egyszerű sync út; async/parallel későbbi kiterjesztés.
* **Minimális felület**: egy stabil vendég export; JSON payload v1-ben.

---

## 4) Host-oldali koncepció (minimál keret)

**Felelősségek:**

* **Modulfeloldás és hívás**: a `type: wasm` csomópont `action` azonosítója alapján az export hívása.
* **Séma-validáció (pre/post)**: `io.in` bemenet megfeleltetése az *accepts* sémának; `io.out` kimenet a *produces* sémának.
* **Limitek**: futásidő-, input-/output-méret-korlátok (sync út).
* **Hibapolitika alkalmazása**: `fail-fast` alap; `on_error: continue` ha megadott.
* **Metrikák (minimál)**: hívás ideje, méret, státusz.

**Nem cél v1-ben:** instance-pool, async callback, WASI-hozzáférés. (Ezek a roadmapben szerepelnek.)

---

## 5) iSDK (guest/wasm) koncepció

**Cél:** a modulfejlesztő egy egyszerű, tiszta felületet kapjon; a WASM-nyűgöket az iSDK viseli.

**„Big Object” (Facade) – **``** a vendégen:**

* A WASM-modul **belsejében** egy `** objektum** végzi *minden* WASM-specifikus műveletet (memória, (de)serializálás, belső init), és **lefuttatja a **`**-t** a megfelelő `op`-pal.
* Publikus metódusai (mind a `Call`-ra építve, op-string nélkül a modulíró felé):

  * `.Init(authCtx, cfg) -> (data_json, error_json)`   ≙ `Call("init", auth, cfg)`
  * `.Process(authCtx, in) -> (data_json, error_json)` ≙ `Call("process", auth, in)`
  * `.Get(authCtx, q) -> (data_json, error_json)`      ≙ `Call("get", auth, q)`
  * `.Notify(authCtx, evt) -> (data_json, error_json)` ≙ `Call("notify", auth, evt)`
* **Visszatérés DEDIKÁLTAN:** **minden** metódus **csak** `(data_json, error_json)`-t ad vissza; nincsenek mellékcsatornák.
* **Belső **``** a WASM-ban:** a `cicwasm.Init(...)` meghívása **a modulon belül is** elérhető, és az első híváskor végzi el a modul-szintű inicializálást (`Call("init", ...)`).

**Determinista működés:**

* WASI és külső I/O **nincs**; csak a kapott `data_json`-ból dolgozik.
* Állapot opcionális; ha van, modulon belül, a `cicwasm` által inicializáltan.

---

### 5/A) iSDK API-szerződés v1

**Műveletek (domain-szint):**

* `init` — létrehozás/konfigurálás (opcionális, modul-élesítés).
* `process` — feldolgozás / beállítás / létrehozás (fő üzemi hívás).
* `get` — aktuális állapot/kimenet lekérése (idempotens, mellékhatás nélkül).
* `notify` — host felé jelzés; v1-ben opcionális stub, v2-ben az async alapja.

**Egységes szignatúra (vendég exporton át, iSDK biztosítja):**

```
Call(op: "init"|"process"|"get"|"notify",
     auth_context_json: JSON,   // opcionális; v1-ben lehet {}
     data_json: JSON)
 -> (data_json: JSON, error_json: JSON|null)
```

* `auth_context_json` a host által előkészített hitelesítési/scope-hint; a tényleges auth a hoston történik.
* `data_json` a domain payload az *accepts/produces* sémához kötve (v1: JSON/UTF-8).

**Hibák (gépi olvasható):**

```json
{
  "code": "INPUT|RUNTIME|INTERNAL|RESOURCE|TIMEOUT",
  "message": "rövid, emberi üzenet",
  "details": { "...": "opcionális gépi adatok" }
}
```

*A kódmező fix, a **`details`** opcionális gépi adatoknak fenntartott hely.*

**Szemantika:**

* v1: **szinkron** hívás (determinista, külső I/O nélkül, WASI off).
* `notify` v1-ben opcionális stub (nincs külső I/O); v2-ben az async visszajelzés host-oldali útja (`job_id`, return path).
* Roadmap: async (job_id, return path), de a fenti API **változatlan** maradhat.
* **Visszatérés dedikált formában:** minden hívás **kizárólag** `(data_json, error_json)` párral tér vissza.

---

## 6) Szerződés és séma (rövid)

* **YAML node kötelező mezők** (`type=wasm|core`): `id`, `action`, `io.in`, `io.out`.
* **Sémák hivatkozása:** a YAML node-ok név/URI alapján hivatkozzák az *accepts/produces* sémát; a host **pre/post** validál.
* ``: nincsen `action`/`io` – csak delegál a `children`-re.
* **Validáció**: strukturális (fa, egyedi `id` testvérek között) és IO-séma (accepts/produces) a hoston.
* **Hibakezelés**: node-onként `on_error: fail|continue`.

---

## 7) Végrehajtási szemantika (sync, v1)

1. A diszpécser **pre-order** bejárja a fát.
2. `type=group` → al-workflow(oka)t indít a gyerekekre; lokális futás nincs.
3. `type=core|wasm` → helyi futás: `io.in` → (hívás) → `io.out`.
4. Hiba esetén a node `on_error` dönt: megáll (`fail`) vagy folytat (`continue`).

---

## 8) DevSec minimumok (v1)

* **Import-allowlist** (WASM host felől), WASI **off**.
* **Korlátok**: idő (pl. ≤ 500 ms), bemeneti/kimeneti méret (pl. ≤ 2 MB) a sync úton.
* **Determinista futás**: nincs külső mellékhatás.
* **Audit/metric** (minimál): ki hívta, mit, meddig tartott, milyen mérettel, mi lett a státusz.

---

## 9) DoD (Definition of Done) – v1

*

---

## 10) Roadmap (v1 → v1.1/v2)

* **v1.1**: CBOR opcionális payload; kiterjesztett hibakódok; finomabb metrikák; **host-oldali "Big Object" kliens** (ugyanazok a metódusok: `Init/Process/Get/Notify`, amelyek a hoston a `Call`-t hívják op-kód megadásával).
* **v2**: instance-pool (host), async (`job_id`), párhuzamosítás a független gyermekekre; WASM vendég `async_return` iSDK-támasz; **opcionális fluent API** (láncolható hívások).

---

## 11) Szószedet

* **iSDK**: guest/wasm oldali SDK a modulfejlesztőknek.
* **Host keret**: executor/diszpécserhez kapcsolódó minimális futtatási réteg.
* **Diszpécser**: core függvény, amely a YAML fa szerinti futást vezérli.
* **Node típusok**: `core` (host függvény), `wasm` (modul), `group` (csak delegál)
* **IO-kötés**: `io.in` → bemenetkulcs, `io.out` → kimenetkulcs.
