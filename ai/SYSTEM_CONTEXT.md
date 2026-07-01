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

A jelenlegi **futó runtime középpontja**: `cmd/relay` + `core/cabinet`.
A `core/nexus` komponensei közül `recorder` és `crypto` aktívan bekötve;
`iac`, `operator`, `sync`, `isolation` implementáltak, de a fő futási útvonalba
még nincs minden bekötve.

---

## A trust ecosystem bootstrap probléma

**Ez a legfontosabb dolog amit tudnod kell.**

A rendszer PKI-alapú hitelesítést igényel (CICRootCA → Intermediate CA → leaf cert),
de ez az ökoszisztéma még nem létezik teljesen. Ezért egyes komponensek
**szándékosan scaffold állapotban vannak** — nem bugok, hanem helyőrzők:

| Komponens | Miért scaffold |
|---|---|
| `pki_verify.go` bekötése | CA lánc még nem bootstrapped |
| `UpstreamSource` TODO | Relay federáció v2 téma |
| `LocalWorker` in-process | 3-process isolation előfeltétele a Vault |

Ha ilyen helyet találsz: **ne kösd be, ne "javítsd ki"** amíg az előfeltétel nem teljesül.

**Ami már implementált (nem scaffold):**

| Komponens | Állapot |
|---|---|
| `Setx` schema validáció | Kész — `validateInputSchema` minden lépésnél fut |
| `graph.go` ExecutionGraph | Kész — lineáris, névvel ellátott kimenet-kulcsok |
| Bootstrap audit log | Kész — `outcome: "skipped"`, `reason: "trust ecosystem not bootstrapped"` |
| `ProofTrace` típus + chain_hash | Kész — per-step hash, chain_hash, recorder wiring |
| Secrets separation | Kész — `loadSecretsFromVault` + `buildRecorderOption(secrets)` |

---

## A ProofTrace

Minden workflow végrehajtásból egy ProofTrace keletkezik:

```
{
  workflow_id:  "deploy@1.0",
  steps: [
    { name: "step1", input_hash: "...", output_hash: "...", module: "..." },
    { name: "step2", input_hash: "...", output_hash: "...", module: "..." },
  ],
  chain_hash:   SHA256(step1.output_hash + step2.output_hash + ...),
  signature:    Vault.Sign(chain_hash),   // ha recorder konfigurált
  timestamp:    UTC
}
```

A `ProofTrace` a `core/cabinet` csomagban él. Recorder nélkül is épül
(csak aláírás marad el) — a Vault hiánya nem fatális.

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

## E2E Smoke Tesztek

```bash
# Indítsd el a relay-t (pl. make build && ./output/<commit>/cic-relay)
RELAY_URL=http://localhost:8080 make test-api
```

A `api_tests/smoke.sh` script a következőket ellenőrzi:
- `GET /` → 200, `{"status":"ok",...}`
- `GET /healthz` → 200, `{"status":"ok"}`
- `GET /readyz` → 200, `{"status":"ok"}`
- `POST /set` üres body → 400, JSON error `{"code":"invalid_request",...}`
- `GET /set` → 405, JSON error `{"code":"method_not_allowed",...}`

A `RELAY_URL` env változóval konfigurálható a cél (default: `http://localhost:8080`).
Csak egy futó relay instance ellen futtatható — nem indít infrastruktúrát.

---

## Dogfooding — Séma-vezérelt önvalidáció

A rendszer induláskor a saját cabinet schema engine-jével validálja a saját konfigurációját.

```
config/relay.config.schema.yaml   ← a konfig séma definíciója (Kind: Schema)
cabinet.ValidateSchema(accepts, data)  ← exportált validátor, $schema kulcs alapján
```

Folyamat:
1. `loadConfig()` betölti az env változókat → `*config` struktúra
   - Range ellenőrzés env parsing közben: `CIC_WASM_EXEC_TIMEOUT_SECONDS` ≥ 1
2. `validateConfig(cfg)` range constraint-eket ellenőriz (MinJSON/WASM ≥ 1, timeout ≥ 1s),
   majd a config-ot `map[string]interface{}`-re konvertálja és hozzáadja `"$schema": "relay.config@1.0"` kulcsot
3. `cabinet.ValidateSchema([]string{"relay.config@1.0"}, configMap)` ellenőrzi a séma ID-t
4. Hiba esetén Fatal — fail-fast, egyértelmű hibaüzenet

Konfigurált env változók (`CIC_*`):
- `CIC_MAX_JSON_INPUT_SIZE` — integer bytes, min 1, default 4 MB
- `CIC_MAX_WASM_MODULE_SIZE` — integer bytes, min 1, default 16 MB
- `CIC_HASHING_TIMEOUT` — duration string, min 1s, default 5s
- `CIC_WASM_EXEC_TIMEOUT_SECONDS` — integer seconds, min 1, default 30

Ez a "dogfooding" elv: a rendszer saját eszközeit használja önmaga validálására.
A séma egyben dokumentáció is — az összes `CIC_*` env változó ott van leírva.

Megjegyzés: a jelenlegi schema validáció `$schema` ID egyezést ellenőriz, nem teljes értékű
JSON Schema validációt — ez a mélység az első lépésként szándékos. A range constraint-ek
explicit Go kódban vannak (`validateConfig` elején).

---

## Release artifact lánc (M9 + M20)

A `make release` target reprodukálható, statikus binárist állít elő a `dist/` könyvtárba:

```bash
make release          # dist/relay + dist/relay.sha256 + dist/sbom.json
make release-verify   # sha256sum -c ellenőrzés
make sourcehash       # source tree hash kinyomtatása (pkg/sourcedigest)
```

A három artifact:
- `dist/relay` — statikus linux/amd64 bináris (`CGO_ENABLED=0`, `-trimpath`, `-s -w`)
- `dist/relay.sha256` — sha256 checksum, `sha256sum -c`-vel ellenőrizhető
- `dist/sbom.json` — CycloneDX JSON SBOM, pinelt tool verzióval (`v1.8.0`)

A `dist/` könyvtár `.gitignore`-ban van — generált artefakt, nem commitolható.

### Build-input boundary (hermetikus build)

A reprodukálható build három rétegből áll:

| Réteg | Mechanizmus | Státusz |
|-------|-------------|---------|
| Forrás tree | `SourceTreeHash` (pkg/sourcedigest, tools/sourcehash) | Kész |
| Go dependency | `go.sum` + `-mod=readonly` | Kész |
| Builder image | digest-pinelt (`golang:1.25.11@sha256:654d46b...`) | Kész |
| CI toolchain | Go `1.25.11` exact, `staticcheck@v0.6.1` | Kész |

**Builder image digests** (docker-compose.yml `&golang_image` anchor, linux/amd64):
- `golang:1.25.11` → `sha256:654d46b58d84d5b0ccd27656ae69c8f2d57341192ee40353cef173e33bef3254`
- `nats:2.10.12` → `sha256:dbab4c53c0de6b089ceb67849d41bdf54a0ad29e50f1105048fc5f19508db803`
- `natsio/nats-box:0.12.0` → `sha256:1a2c9c9281add7b81ccc886f92d2bc7a247c3ddfd99ecfab51f86a883b38cf7d`

Re-pin parancs:
```bash
docker manifest inspect <image>:<tag> | jq '.manifests[] | select(.platform.os=="linux" and .platform.architecture=="amd64") | .digest'
```

Build metaadatok ldflags-szal injektálva (`BuildID`, `CommitHash`, `Timestamp`, `SourceTreeHash`), induláskor INFO log jelenik meg róluk.

---

## Mérce

```
make test    # unit + integráció
make verify  # golden fájlok egyezése — ez a bíró
```

A `context/CONTRACT.md`, `context/SYMBOLS.md`, `context/LIMITS.md` kanonikus igazságok.
Ha ezekkel konfliktusban vagy: javasolj contract-diffet, ne térj el csendben.
