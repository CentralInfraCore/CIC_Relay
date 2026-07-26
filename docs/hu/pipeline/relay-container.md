# A relay futtatása CI-végrehajtóként

Ez a dokumentum azt írja le, hogyan kell a relayt konténerben megépíteni, elindítani
és vezérelni, hogy a „itt egy repo és egy commit" bemenetből aláírt artifact legyen.

Korábban a relay indítása kézzel összerakott `docker run` volt, több nem kézenfekvő
kapcsolóval. Ezek most a `Dockerfile.relay`-be, a `docker-compose.yml` `relay`
szolgáltatásába és a `relay.*` make célokba vannak beépítve.

## Gyors kezdés

```bash
make relay.build          # image megépítése
make relay.up             # indítás, várakozás a /readyz-re
make relay.status         # health + megy-e a ProofTrace rögzítés

make relay.ci \
  REPO_URL=https://github.com/org/repo \
  COMMIT=b0d1287abbb67a3ef79bfff27a5cc6b6a98ad5ee \
  SCHEMA=postgresql VERSION=v0.18.0
```

A `relay.ci` kiírja a `build_hash`, `verification_root`, `source_ref`,
`source_tree_digest`, `cic_sign`, `chain_hash` és `source_digest` értékeket.

Ha azt akarod tudni, **melyik revízió** épült, a `verification_root`-ot nézd, ne a
`build_hash`-t — az utóbbi `sha256(raw_artifact)`, tiszta tartalom-digest. Lásd:
[source-provenance.md](source-provenance.md).

## Konfiguráció

A `relay` szolgáltatás ezeket olvassa a környezetből (a `docker-compose.yml` melletti
`.env` fájl is jó):

| Változó | Alapérték | Mire való |
|---|---|---|
| `CIC_RELAY_PORT` | `8080` | Publikált port |
| `CIC_BUILD_DIR` | `./build` | Klón/build könyvtár — **lásd lentebb** |
| `CIC_AUDIT_DIR` | `./relay-audit` | ProofTrace audit git repó |
| `CIC_SECRETS_DIR` | `./relay-secrets` | Csak olvashatóként `/run/secrets/relay` alá |
| `CIC_BUILDER_CONTAINER` | `cic-py-builder` | Ebben a konténerben futnak a `make` célok |
| `VAULT_ADDR` | — | pl. `https://host.docker.internal:18200` |
| `VAULT_TOKEN` | — | **Enélkül nincs aláírt ProofTrace** |
| `VAULT_CACERT` | — | pl. `/run/secrets/relay/vault-ca.crt` |
| `CIC_VAULT_MOUNT` | `transit` | Vault transit mount |
| `CIC_VAULT_KEY` | — | Aláíró kulcs; a tokennek **erre a kulcsra** kell jogosultnak lennie |
| `CIC_TRUST_MODE` | `dev` | PKI trust mód |

### A `CIC_BUILD_DIR`-t meg kell osztani a builder konténerrel

A relay ide klónozza a repót, majd `docker exec <builder_container> make <target>`-et
futtat. A builder külön konténer: ha nem **ugyanazt a hoszt-útvonalat** mountolja, nem
látja a klónt, és a `make` üres vagy elavult könyvtáron fut.

A builder munkakönyvtárának is a klón helyére kell mutatnia, mert a relay `-w` nélkül
hívja a `docker exec`-et.

### Vault

Két hiba hasonlónak látszik, de nem az:

- **Nincs `VAULT_TOKEN`** — a relay elindul, és `ProofTrace recording disabled`-et
  logol. Minden működik, csak aláírt trace nem keletkezik. A `make relay.status`
  ezt külön jelzi.
- **Van token, de nincs joga a `CIC_VAULT_KEY`-hez** — a futás az aláíró lépésnél
  hasal el Vault `403 permission denied`-del. Egy token lehet teljesen érvényes, és
  közben épp arra a kulcsra nincs jogosultsága.

A Vault jellemzően a hoszton fut. A konténer bridge hálózaton van, tehát a
`127.0.0.1` nem éri el; a szolgáltatás ezért deklarálja a
`host.docker.internal:host-gateway`-t — használd a
`VAULT_ADDR=https://host.docker.internal:<port>` formát.

## Amit az image magába zár

Ezek voltak a kézi összerakás buktatói; mindegyiket az image kezeli, nem az üzemeltető:

| Tünet | Ok | Mi kezeli |
|---|---|---|
| `error obtaining VCS status: exit status 128` | a Go VCS-stampingje elhasal, ha a build kontextusban nincs használható `.git` (CI checkout, RO mount) | `-buildvcs=false` a build stage-ben |
| `git clone … detected dubious ownership` | a klón célja más uid tulajdona, mint a folyamaté | `/etc/gitconfig` `safe.directory = *`-gal, `GIT_CONFIG_GLOBAL` beállítva. Az env-változós forma (`GIT_CONFIG_COUNT`/`KEY_0`/`VALUE_0`) itt **nem** működik |
| `docker exec: executable file not found in $PATH` | a pipeline `docker`-t hív, ami egy Go alap-image-ben nincs | docker **kliens** a `docker:28-cli`-ből másolva; a démon a hoszté, a mountolt socketen át |
| `mkdir /var/lib/cic-relay: permission denied` | az audit recordernek írható út kell | a könyvtár az image-ben létrejön, volume-ként kiajánlva |
| `cic.pipeline.start: build_dir is required` | nincs alapértelmezett build könyvtár | `CIC_SCHEMA_BUILD_DIR=/build` |
| `cic.pipeline.test: builder_container is required` | nincs alapértelmezett builder konténer | `CIC_BUILDER_CONTAINER` a compose szolgáltatásban |

## Megjegyzések

- A szolgáltatás a `relay` compose profil mögött van, tehát **nem indul el** a
  `docker compose up -d`-vel, és a meglévő build/tooling stacket nem érinti.
- Az image-ben nincs `curl`/`wget`, és nem deklarál `HEALTHCHECK`-et; a `/healthz`
  és `/readyz` végpontokat kívülről kell pollozni, ahogy a `make relay.up` teszi.
- A `CommitHash` / `SourceTreeHash` build argumentumok a **relay saját** forrását
  írják le. Ezek csak akkor kerülnek ProofTrace-be, ha a futás nem hordoz saját
  repót — lásd [source-provenance.md](source-provenance.md).
