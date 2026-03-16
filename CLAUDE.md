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

## A legfontosabb dolog

**A scaffold-elemek szándékosak.**

A trust ecosystem (CICRootCA → Intermediate CA → leaf cert) még nincs bootstrapped.
Ezért egyes komponensek tudatosan scaffold állapotban vannak — nem bugok:

- `pki_verify.go` bekötése: vár a CA lánc bootstrap-re
- `UpstreamSource` TODO: relay federáció, v2 téma
- `LocalWorker` in-process: 3-process isolation előfeltétele a Vault

Ne kösd be, ne "javítsd" ezeket amíg az előfeltétel nem teljesül.

**Ami már implementált (nem placeholder):**
- `Setx` schema validáció: `validateInputSchema` aktívan fut minden lépésnél
- `ExecutionGraph`: lineáris, névvel ellátott kimenet-kulcsokkal, visszafelé kompatibilis
- `ProofTrace`: per-step hash, chain_hash, recorder wiring
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

| Fájl | Mit csinál |
|---|---|
| `core/cabinet/service.go` | `Setx()` workflow végrehajtó, schema validáció, ProofTrace, recorder hívás |
| `core/cabinet/graph.go` | `ExecutionGraph` + `ParseExecutionGraph` — lineáris, névvel ellátott lépések |
| `core/cabinet/cicwasm.go` | WASM ABI: `Call/allocate/deallocate`, LRU cache, singleflight, timeout |
| `core/cabinet/validate.go` | Determinisztikus checksum (kind prefix + canonical JSON + SHA256) |
| `core/cabinet/stores.go` | CAS-alapú ModuleStore, thread-safe SchemaStore/WorkflowStore |
| `core/cabinet/pki_verify.go` | X.509 chain verify + CRL + SPKI pinning (bekötésre vár) |
| `core/nexus/recorder/recorder.go` | Git-alapú audit: expected+actual JSON + Vault aláírás → commit |
| `core/nexus/crypto/service.go` | Vault Transit engine wrapper |
| `core/nexus/isolation/coordinator.go` | 3-lépéses onion decrypt (Relay→Module→Host) |
| `pkg/canonicaljson/canonicaljson.go` | Determinisztikus JSON serializer |
| `cmd/relay/bootstrap.go` | YAML komponens betöltő, PKI asset loader |
| `cmd/relay/main.go` | HTTP server, config, buildRecorderOption, loadSecretsFromVault |
| `tools/symbolsgen/main.go` | `SYMBOLS.md` generátor (API contract enforcement) |

---

## Következő lépések (sprint 4, prioritás szerint)

1. **ci-quality-gates** — lint/vet/coverage-threshold CI-ban (M7.2-M7.4)
2. **limit-schema-config** — LIMITS schema-vezérelt, `CIC_WASM_EXEC_TIMEOUT_SECONDS` (M4.4)
3. **circuit-breaker-rate-limit** — rate limiter + concurrent cap + slow-loris (M9)
4. **operator-bootstrap-lifecycle** — HMAC token bootstrap + onboard/offboard (M16.2-3)
5. **nexus-upstream-source** — UpstreamSource HTTP implementáció (M11)

Részletek és elfogadási kritériumok: `ai/PROMPTMAP.yaml`

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
- Induláskor ellenőrizd: `mcp__cic-graph__kb_status`
