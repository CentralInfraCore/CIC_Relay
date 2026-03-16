# Onboarding (AI)

## 1 perc alatt

- **Mi ez:** ProofTrace-alapú auditálható végrehajtási platform. Minden workflow determinisztikus hash-láncot alkot.
- **Entrypoint:** `cmd/relay/main.go` — HTTP API + bootstrap. Nincs `cmd/nexus/main.go`.
- **Szerződés:** `context/CONTRACT.md` — ez az I/O és invariáns igazság.
- **Szimbólumok:** `context/SYMBOLS.md` — publikus API definíció.
- **Mérce:** `make verify` — golden fájl egyezés. Ez a bíró.
- **Limitek:** `context/LIMITS.md` — teljesítmény és memória keretek.

## Mielőtt bármit írsz

Olvasd el: `ai/SYSTEM_CONTEXT.md`

**Scaffold vs. implementált** — a kettő nem ugyanaz:

| Scaffold (ne nyúlj hozzá) | Miért |
|---|---|
| `pki_verify.go` bekötése | CA lánc még nem bootstrapped |
| `UpstreamSource` | Relay federáció v2 téma |
| `LocalWorker` in-process | 3-process isolation előfeltétele a Vault |

| Már implementált (ne írd felül) | Állapot |
|---|---|
| `Setx` schema validáció | `validateInputSchema` fut minden lépésnél |
| `graph.go` ExecutionGraph | Lineáris gráf, névvel ellátott kimenet-kulcsok |
| Bootstrap audit log | `outcome: "skipped"`, `reason: "trust ecosystem not bootstrapped"` |
| ProofTrace + recorder wiring | Per-step hash, chain_hash, Vault aláírás opcionális |
| Secrets separation | `loadSecretsFromVault` + `buildRecorderOption(secrets)` |

## A három réteg

```
cmd/relay      → HTTP API (:8080), bootstrap, PKI asset betöltés
core/cabinet   → Schema/Module/Workflow + WASM végrehajtás (cicwasm.go, service.go)
core/nexus     → IaC, recorder (Git+Vault), crypto, sync, isolation, operator
```

Jelenlegi futó runtime: `cmd/relay` + `core/cabinet`, opcionálisan `nexus/recorder` + `nexus/crypto`.

## Hogyan ellenőrzöl

```bash
make test      # unit + integráció
make verify    # golden fájlok — ez zölden kell álljon
make test-api  # E2E smoke (futó relay instance kell)
```

## Ha gond van

Javasolj contract-diffet (`context/CONTRACT.md`-hoz), ne térj el csendben.