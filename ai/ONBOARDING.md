# Onboarding (AI)

## 1 perc alatt

- **Mi ez:** ProofTrace-alapú auditálható végrehajtási platform. Minden workflow determinisztikus hash-láncot alkot.
- **Szerződés:** `context/CONTRACT.md` — ez az I/O és invariáns igazság.
- **Szimbólumok:** `context/SYMBOLS.md` — publikus API definíció.
- **Mérce:** `make verify` — golden fájl egyezés. Ez a bíró.
- **Limitek:** `context/LIMITS.md` — teljesítmény és memória keretek.

## Mielőtt bármit írsz

Olvasd el: `ai/SYSTEM_CONTEXT.md`

A legfontosabb: a **placeholder-ek szándékosak**. A trust ecosystem (CICRootCA, CA lánc)
még nincs bootstrapped — ezért egyes bekötések szándékosan hiányoznak.
Ne "javítsd ki" amit nem értesz — kérdezz.

## A három réteg

```
cmd/relay      → HTTP API, bootstrap, PKI asset betöltés
core/cabinet   → Schema/Module/Workflow + WASM végrehajtás (cicwasm.go, service.go)
core/nexus     → IaC, recorder (Git+Vault), crypto, sync, isolation, operator
```

## Hogyan ellenőrzöl

```bash
make test      # unit + integráció
make verify    # golden fájlok — ez zölden kell álljon
```

## Ha gond van

Javasolj contract-diffet (`context/CONTRACT.md`-hoz), ne térj el csendben.