# CIC-Relay — Claude kontextus

## Mi ez a rendszer

A CIC-Relay egy **ProofTrace-alapú, auditálható végrehajtási platform**.
Minden végrehajtott workflow determinisztikus hash-láncot alkot,
amelynek integritása a CICRootCA-ig visszavezethető kriptográfiai aláírással igazolható.

A bizalom nem feltételezés — igazolható állapot.

Részletes architektúra: `ai/SYSTEM_CONTEXT.md`
Mérföldkövek és állapot: `ai/ROADMAP.md`
Következő konkrét lépések: `ai/PROMPTMAP.yaml`

---

## Boot sequence — minden session elején

Mielőtt szakmai kérdésre válaszolsz, végezd el ezt a sorrendet:

1. `mcp__cic-graph__kb_status` — tudásbázis elérhető és friss?
2. Kanonikus igazságok: `context/CONTRACT.md`, `context/SYMBOLS.md`, `context/LIMITS.md`
3. Scaffold térkép: mi **implemented**, mi **scaffold**, mi **concept** (lásd lent)
4. Bridge térkép: hol van élő összeköttetés koncepció és runtime között, hol nincs

Amíg ez a négy pont nincs meg, ne tegyél tényállításokat az architektúráról.

---

## Háromszintű státusz — minden állításhoz kötelező

Minden komponensre, fogalomra, döntésre jelöld meg:

| Státusz | Jelentés |
|---|---|
| **implemented** | kódban él, tesztek fedik, CI zöld |
| **scaffold** | kódban van, de szándékosan bekötetlen — előfeltétel hiányzik |
| **concept** | dokumentált, de még nincs runtime megfelelője |

**Ha a státuszt nem tudod node- vagy file-szinten alátámasztani, ne mondd ki tényként.**

Scaffold nem hiba. A scaffold-elemek szándékosak és előfeltételhez kötöttek.

---

## Scaffold térkép (aktuális)

| Komponens | Státusz | Előfeltétel |
|---|---|---|
| `pki_verify.go` bekötése | **scaffold** | CICRootCA → Intermediate CA → leaf cert bootstrap |
| `UpstreamSource` | **scaffold** | relay federáció (v2) |
| `LocalWorker` in-process | **scaffold** | 3-process isolation + Vault |
| quorum döntési réteg | **concept** | nincs runtime megfelelő |
| CICmeta protocol | **concept** | nincs élő bridge a Relay runtime-hoz |

---

## Implemented — ami tényleg fut

- `Setx` schema validáció: `validateInputSchema` aktívan fut minden lépésnél
- `ExecutionGraph`: lineáris, névvel ellátott kimenet-kulcsokkal, visszafelé kompatibilis
- `ProofTrace`: per-step hash, chain_hash, recorder wiring
- `core/cabinet` WASM runtime: wazero, LRU cache, singleflight, timeout
- Bootstrap audit log: `outcome: "skipped"` + `reason: "trust ecosystem not bootstrapped"`

---

## Három réteg

```
cmd/relay        HTTP API (:8080), bootstrap, PKI asset betöltés
core/cabinet     Schema/Module/Workflow registry + WASM végrehajtó (wazero)
core/nexus       IaC loader, Git recorder, Vault crypto, sync, isolation, operator
```

A `cabinet` és `nexus` szándékosan független — különböző fejlődési ütemük van.
A jelenlegi futó runtime: `cmd/relay` + `core/cabinet`, opcionális `nexus/recorder` + `nexus/crypto`.

---

## Kulcs komponensek

| Fájl | Státusz | Mit csinál |
|---|---|---|
| `core/cabinet/service.go` | implemented | `Setx()` workflow végrehajtó, schema validáció, ProofTrace, recorder hívás |
| `core/cabinet/graph.go` | implemented | `ExecutionGraph` + `ParseExecutionGraph` — lineáris, névvel ellátott lépések |
| `core/cabinet/cicwasm.go` | implemented | WASM ABI: `Call/allocate/deallocate`, LRU cache, singleflight, timeout |
| `core/cabinet/validate.go` | implemented | Determinisztikus checksum (kind prefix + canonical JSON + SHA256) |
| `core/cabinet/stores.go` | implemented | CAS-alapú ModuleStore, thread-safe SchemaStore/WorkflowStore |
| `core/cabinet/pki_verify.go` | scaffold | X.509 chain verify + CRL + SPKI pinning — vár a CA lánc bootstrap-re |
| `core/nexus/recorder/recorder.go` | implemented | Git-alapú audit: expected+actual JSON + Vault aláírás → commit |
| `core/nexus/crypto/service.go` | implemented | Vault Transit engine wrapper |
| `core/nexus/isolation/coordinator.go` | implemented | 3-lépéses onion decrypt (Relay→Module→Host) |
| `pkg/canonicaljson/canonicaljson.go` | implemented | Determinisztikus JSON serializer |
| `cmd/relay/bootstrap.go` | implemented | YAML komponens betöltő, PKI asset loader |
| `cmd/relay/main.go` | implemented | HTTP server, config, buildRecorderOption, loadSecretsFromVault |
| `tools/symbolsgen/main.go` | implemented | `SYMBOLS.md` generátor (API contract enforcement) |

---

## Bridge térkép — hol szakad meg a lánc

A rendszernek két koherens félrendszere van, de köztük egyes hidak még nem élők:

```
concept/CICmeta protocol  ──✗──  core/cabinet runtime
concept/quorum            ──✗──  bármely runtime component
pki_verify.go             ──✗──  CICRootCA chain (nincs bootstrapped)
UpstreamSource            ──✗──  HTTP upstream (v2)
```

Ha egy kérdés ilyen pontra mutat: ne mondd, hogy "nincs" — mondd, hogy
**"a modell létezik, de az implementációs híd itt megszakad"**.

---

## Reasoning mód

Válasz előtt azonosítsd a módot:

- **immersion**: fogalmak, relációk, rendszerlogika befogadása — ne javasolj, ne kritizálj
- **audit**: státusz, bridge-hiányok, contract-eltérések ellenőrzése
- **implementation**: konkrét kódváltozás, test, CI

Immersion módban tilos hiányt feltételezni ott, ahol scaffold szándékos.

---

## Graph-first reasoning

MCP kérdéseknél ne `search_query → snippet → válasz` sorrendben dolgozz.

Helyette:
1. fogalom azonosítás → induló node-ok
2. 1–2 hop szomszédok (`neighbors`, `guided_path`)
3. státusz ellenőrzés (implemented/scaffold/concept)
4. bridge ellenőrzés (van-e code/runtime pointer)
5. csak ebből válasz

---

## Következő lépések (sprint 15, prioritás szerint)

1. **cmd-relay-coverage** — `buildOperatorIfConfigured` + `buildRecorderOption` injectable hook, 80%→90% (p54)
2. **go-125-upgrade** — Go 1.24.6 → 1.25.x; `govulncheck` hard gate feloldása CI-ban
3. **upstream-source-http** — `UpstreamSource` HTTP implementáció relay federáció v2-höz (M37)

Sprint 4–14 összes task Done. Részletek: `ai/ROADMAP.md` Sprint 15 szekció.

---

## Determinizmus — alapkövetelmény

- `pkg/canonicaljson`: rendezett kulcsok, platform-független
- WASM: `wabin` canonical strip (custom sections eltávolítva)
- `CanonicalizeSetPayload`: dedup+sort a bemeneten
- `metaForChecksum`: csak stabil mezők (Checksum/Signature/Valid kizárva)

Ha hash-elésbe kerülő dolgot módosítasz: `make verify` golden tesztek kötelezők.

---

## Mérce

```bash
make test      # unit + integráció
make verify    # golden fájl egyezés — ez a bíró
```

Kanonikus igazságok: `context/CONTRACT.md`, `context/SYMBOLS.md`, `context/LIMITS.md`

Ha ezekkel konfliktusban vagy: javasolj contract-diffet, ne térj el csendben.

---

## MCP szerver

A `.mcp.json` konfigurálja a `cic-graph` MCP szervert:
- KB adatok: `MCPs/private/kb_data/pkl`
- Teljes CIC ökoszisztéma tudásbázis elérhető (`search_query`, `get_node`, `find_nodes` stb.)
- Induláskor: `mcp__cic-graph__kb_status` — ha nem elérhető, a KB-alapú állítások nem megbízhatók

---

## Döntéstörténet

A repo önmagában nem teljes kontextus. A fogalmak kialakulásának history-ja a `theads/` könyvtárban él (916 thread, 2025-03-04-től).

Kulcs döntési pontok:
- **CICmeta naming** (2025-11-04): `CICmeta` = technikai elszámolási egység, `CIC` = márkanév — egy userüzenettel vált szét
- **porszívózás módszer** (2025-06-26): AI-taníthatóság, graph-first feldolgozás, immersion mód

Ha egy fogalom eredetét vagy névadási döntését keresed: a thread-ek a döntéstörténet elsődleges forrásai.
