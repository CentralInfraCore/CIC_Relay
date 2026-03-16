# System Context (AI számára)

Ez a fájl az architektúra és a tervezési döntések összefoglalója AI együttműködőknek.
Olvasd el mielőtt bármit módosítasz.

---

## Mi ez a rendszer?

A CIC-Relay egy **ProofTrace-alapú, auditálható végrehajtási platform**.
Nem egyszerű infrastruktúra-kezelő — hanem egy olyan rendszer, ahol minden végrehajtott
workflow determinisztikus hash-láncot alkot, és a lánc kriptográfiailag bizonyítható
a CICRootCA-ig visszavezethetően.

A bizalom nem feltételezés, hanem igazolható állapot.

---

## A három réteg

```
cmd/relay          HTTP belépési pont, bootstrap, PKI asset betöltés
core/cabinet       Végrehajtó réteg: Schema/Module/Workflow registry + WASM runtime
core/nexus         Control plane: IaC loader, recorder, crypto, sync, isolation, operator
```

A `cabinet` és a `nexus` szándékosan független — különböző fejlődési ütemük van.

---

## A trust ecosystem bootstrap probléma

**Ez a legfontosabb dolog amit tudnod kell.**

A rendszer PKI-alapú hitelesítést igényel (CICRootCA → Intermediate CA → leaf cert),
de ez az ökoszisztéma még nem létezik teljesen. Ezért egyes komponensek
**szándékosan scaffold állapotban vannak** — nem bugok, hanem helyőrzők:

| Komponens | Miért scaffold |
|---|---|
| `pki_verify.go` bekötése | CA lánc még nem bootstrapped |
| `Setx` schema validáció TODO | Schema ökoszisztéma nem definiált |
| `graph.go` üres | ExecutionGraph tervezés folyamatban |
| `UpstreamSource` TODO | Relay federáció v2 téma |
| `LocalWorker` in-process | 3-process isolation előfeltétele a Vault |

Ha ilyen helyet találsz: **ne kösd be, ne "javítsd ki"** amíg az előfeltétel nem teljesül.

---

## Amit SZABAD és KELL csinálni

- A `core/cabinet` rétegen belül: schema validáció bekötése a `Setx`-be
- `ProofTrace` típus bevezetése — a workflow végrehajtás kriptográfiai lenyomata
- `Setx` → `recorder` összekötése — hogy a végrehajtás auditálható legyen
- Lineáris `ExecutionGraph` a `graph.go`-ba — DAG nélkül, lépés→kimenet névvel
- Az egyetlen valódi bug: `bootstrap.go`-ban `"outcome": "success"` → `"outcome": "skipped"`

---

## A ProofTrace fogalma

Minden workflow végrehajtásból egy ProofTrace keletkezik:

```
{
  workflow_id:  "deploy@1.0",
  steps: [
    { name: "step1", input_hash: "...", output_hash: "...", module: "..." },
    { name: "step2", input_hash: "...", output_hash: "...", module: "..." },
  ],
  chain_hash:   SHA256(step1.output_hash + step2.output_hash + ...),
  signature:    Vault.Sign(chain_hash),
  timestamp:    UTC
}
```

Ez az entitás még nem létezik a kódban — ez a következő implementálandó egység.

---

## Determinizmus mint alapkövetelmény

Minden hash-elt kimenetnek determinisztikusnak kell lennie:
- `pkg/canonicaljson` gondoskodik a JSON rendezésről
- WASM modulok `wabin`-nal kanonizálva (custom sections eltávolítva)
- `CanonicalizeSetPayload` dedup+sort a bemeneten
- `metaForChecksum` csak stabil mezőket hash-el

Ha valamit módosítasz ami hash-elésbe kerül: ellenőrizd a GOLDEN tesztekkel.

---

## Fejlesztői Vault bootstrap

A `core/nexus/crypto` Vault Transit aláírást igényel. Fejlesztői környezethez:

```bash
make dev-vault        # elindít egy lokális in-memory Vault-ot (host-side, vault bináris kell)
source $XDG_RUNTIME_DIR/cic-vault-dev.env  # env változók betöltése
make test             # tesztek (mock-ok, nem igénylik a Vault-ot)
make verify           # golden fájlok egyezése — mindig futtatható Vault nélkül is
make dev-vault-stop   # leállítás
```

Fontos tudnivalók:
- `make dev-vault` **host-oldalon** fut (nem Dockerben) — a `vault` binárisnak elérhetőnek kell lennie
- Token: `dev-root-token`, cím: `http://127.0.0.1:18200`
- Transit kulcs neve: `cic-dev-sign-key`
- `make verify` (golden tesztek) és `make test` (unit/integrációs, mock-okkal) **Vault nélkül is futtatható**

---

## Dogfooding — Séma-vezérelt önvalidáció

A rendszer induláskor a saját cabinet schema engine-jével validálja a saját konfigurációját.

```
config/relay.config.schema.yaml   ← a konfig séma definíciója (Kind: Schema)
cabinet.ValidateSchema(accepts, data)  ← exportált validátor, $schema kulcs alapján
```

Folyamat:
1. `loadConfig()` betölti az env változókat → `*config` struktúra
2. `validateConfig(cfg)` a config-ot `map[string]interface{}`-re konvertálja
   és hozzáadja `"$schema": "relay.config@1.0"` kulcsot
3. `cabinet.ValidateSchema([]string{"relay.config@1.0"}, configMap)` ellenőrzi
4. Hiba esetén Fatal — fail-fast, egyértelmű hibaüzenet

Ez a "dogfooding" elv: a rendszer saját eszközeit használja önmaga validálására.
A séma egyben dokumentáció is — az összes `CIC_*` env változó ott van leírva.

---

## Mérce

```
make test    # unit + integráció
make verify  # golden fájlok egyezése — ez a bíró
```

A `context/CONTRACT.md`, `context/SYMBOLS.md`, `context/LIMITS.md` kanonikus igazságok.
Ha ezekkel konfliktusban vagy: javasolj contract-diffet, ne térj el csendben.