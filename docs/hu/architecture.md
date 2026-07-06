# Rendszer Architektúra Áttekintés

Ez a dokumentum a projekt magas szintű architektúráját és működési filozófiáját mutatja be. A cél, hogy egy új fejlesztő 5-10 perc alatt megértse a rendszer alapvető koncepcióit.

> Ez a repo eredetileg a közös `base-repo` scaffolding template-ből lett
> klónozva, és ez a dokumentum korábban *annak* a template-nek a saját
> "sablonokat gyárt a leszármazott repóknak" modelljét írta le, nem azt,
> amit a CIC-Relay ténylegesen csinál. A `project.yaml`/`tools/compiler.py`
> még mindig a `base-repo` eredeti template-metaadatait hordozza, és nincs
> bekötve ennek a repónak a CI-jába — örökölt, de inaktív scaffolding,
> nem része az alább leírt runtime-nak.

## Mi a CIC-Relay

A CIC-Relay egy **ProofTrace-alapú, auditálható végrehajtási platform**.
Minden végrehajtott workflow determinisztikus hash-láncot alkot, amelynek
integritása a CICRootCA-ig visszavezethető kriptográfiai aláírással
igazolható. A bizalom itt nem feltételezés — igazolható állapot.

## Három réteg

```
cmd/relay        HTTP API (:8080), bootstrap, PKI asset betöltés
core/cabinet      Schema/Module/Workflow registry + WASM végrehajtó (wazero)
core/nexus        IaC loader, Git recorder, Vault crypto, sync, isolation, operator
```

A `cabinet` és `nexus` szándékosan független — különböző fejlődési ütemük
van. A jelenlegi ténylegesen futó runtime a `cmd/relay` + `core/cabinet`,
opcionálisan bekötött `nexus/recorder` és `nexus/crypto` komponensekkel.

## Végrehajtási folyamat

1. Egy kliens `POST`-ol egy workflow-kérést a `cmd/relay` `/set` végpontjára.
2. A `core/cabinet` `Setx()` függvénye validálja a kérést a regisztrált séma
   ellen (`validateInputSchema`), majd végigmegy az `ExecutionGraph`-on — egy
   lineáris, névvel ellátott lépéssorozaton.
3. Minden WASM modult megnevező lépés a `cicwasm` host-on keresztül fut: a
   wazero runtime lefordítja/instanciálja a modult (LRU-cache-elt,
   singleflight-deduplikált, timeout-korlátos), majd végrehajtja.
4. Minden lépés bemenete/kimenete belekerül egy lépésenkénti `ProofTrace`-be,
   ami egy futó `chain_hash`-be láncolódik — ez a determinisztikus,
   auditálható rekordja annak, hogy mi futott ténylegesen.
5. Ha van konfigurált recorder (`core/nexus/recorder`), a trace-t a Vault
   aláírja, és egy Git audit logba kerül commitolásra. Ha nincs, a rekord
   `outcome: "skipped", reason: "trust ecosystem not bootstrapped"` értékkel
   készül el — a bootstrap szándékosan hiányos, amíg a PKI-lánc nem épül fel
   (lásd a Státusz szekciót lent), és a rendszer ezt ki is mondja, ahelyett
   hogy úgy tenne, mintha kész lenne.

## Determinizmus

A teljes bizalmi modell azon múlik, hogy azonos bemenetek byte-azonos
hash-eket adjanak, ezért a kanonizáció végig érvényesítve van:

- `pkg/canonicaljson` — rendezett kulcsok, platform-független szerializáció.
- A WASM binárisok custom section-jei kanonikusan el vannak távolítva
  (`wabin`) hash-elés előtt.
- A `CanonicalizeSetPayload` dedupe-olja és rendezi a kérés payload-ját,
  mielőtt az egyáltalán hash-elésre kerülne.

Bármilyen változtatás, ami hash-elésbe kerülő dolgot érint, köteles átmenni
a golden teszteken: `make verify`.

## Státusz-jelölés

Minden komponens ebben a rendszerben a három közül az egyik, és a
különbségnek jelentősége van — a "scaffold" nem hiba, hanem szándékos,
előfeltételhez kötött helyfoglaló:

| Státusz | Jelentés |
|---|---|
| **implemented** | kódban él, tesztek fedik, CI zöld |
| **scaffold** | kódban van, de szándékosan bekötetlen — előfeltétel hiányzik |
| **concept** | dokumentált, de még nincs runtime megfelelője |

### Ami ma implementált

- `core/cabinet/service.go` — `Setx()` workflow végrehajtó, schema validáció, ProofTrace, recorder hívás
- `core/cabinet/graph.go` — `ExecutionGraph` (lineáris, névvel ellátott kimenet-kulcsokkal, visszafelé kompatibilis)
- `core/cabinet/cicwasm.go` — WASM ABI (`Call`/`allocate`/`deallocate`), LRU cache, singleflight, timeout
- `core/cabinet/validate.go` — determinisztikus checksum (kind prefix + canonical JSON + SHA-256)
- `core/cabinet/stores.go` — CAS-alapú `ModuleStore`, thread-safe `SchemaStore`/`WorkflowStore`
- `core/nexus/recorder/recorder.go` — Git-alapú audit: expected+actual JSON + Vault aláírás → commit
- `core/nexus/crypto/service.go` — Vault Transit engine wrapper
- `core/nexus/isolation/coordinator.go` — 3-lépéses onion decrypt (Relay → Module → Host)
- `pkg/canonicaljson` — determinisztikus JSON serializer
- `pkg/obs` — strukturált audit-log kibocsátás (`LogRecord`, hash-láncolt integrity envelope), plusz
  alap futásidejű metrikák: egy in-process aggregátor (`obs.PrometheusSink`), amely a `GET /metrics`
  végponton érhető el

### Ami scaffold (szándékos, előfeltételre vár)

| Komponens | Előfeltétel |
|---|---|
| `pki_verify.go` bekötése | CICRootCA → Intermediate CA → leaf cert bootstrap |
| `UpstreamSource` | relay federáció (v2) |
| `LocalWorker` in-process isolation | 3-process isolation + Vault |

### Ami csak concept (dokumentált, még nincs runtime)

- A quorum döntési réteg
- A CICmeta protocol híd a `core/cabinet` felé

Ha egy kérdés ilyen pontra mutat, a becsületes válasz sosem az, hogy "nincs"
— hanem hogy "a modell létezik, de az implementációs híd itt megszakad".

## Hova tovább

- `CLAUDE.md` — a kanonikus AI/fejlesztői kontextus, minden sprintben frissítve
- `context/CONTRACT.md`, `context/SYMBOLS.md`, `context/LIMITS.md` — kanonikus szerződések
- `ai/ROADMAP.md` — mérföldkövek és a jelenlegi sprint állapota
- `ai/PROMPTMAP.yaml` — a konkrét következő lépések
