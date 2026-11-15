# CIC Relay

A CIC-Relay egy **ProofTrace-alapú, auditálható végrehajtási platform**.
Minden végrehajtott workflow determinisztikus hash-láncot alkot,
amelynek integritása kriptográfiailag igazolható.

A bizalom nem feltételezés — igazolható állapot.

---

## Állapot és biztonsági helyzet

A CIC Relay jelenleg **referencia- és PoC-implementáció**. Nem hardened, és nem production-használatra készült. Az alábbi korlátok ismertek; a javításuk a fejlesztési ütemterv része, nem kész funkció:

- **A HTTP API-nak nincs hitelesítése és TLS-e.** Ne tegye ki hálózatra. A `docker-compose.yml` alapértelmezetten csak a loopback interfészre köti (`CIC_RELAY_BIND`).
- **Az aláíratlan vagy hibás aláírású modul nem töltődik be**: `prod`-ban és alapértelmezésben `dev`-ben is elutasított. Kizárólag `dev`-ben, a `CIC_ALLOW_UNSIGNED_MODULES=1` explicit operátori opt-innel tölthető be az **aláírásfájl nélküli** modul, capability nélkül; érvénytelen aláírást az opt-in sem fogad el. Részletek: [`docs/hu/security/module-loading.md`](docs/hu/security/module-loading.md). A `cic-flow` és a `git` host függvények csak ellenőrzött (CICSourceCA-provenance-szel rendelkező) modulnak érhetők el. A `git` hozzáférést a modul aláírt manifestje modulonként, repóra, műveletre, remote-ra és ref-re szűkítve adja meg, a Relay minden hívásnál ellenőrzi; a `git.push` vendégmodulok számára visszavonva. Betöltéskor a manifestben deklarált importok ellenőrződnek, a WASM bináris tényleges importlistája nem. Részletek: [`docs/hu/security/git-host-functions.md`](docs/hu/security/git-host-functions.md). A korábbi állapot (argumentum-injekció, modulonkénti jogosultság nélkül) biztonsági hiba volt: [GHSA-5489-jhgc-m947](https://github.com/CentralInfraCore/CIC_Relay/security/advisories/GHSA-5489-jhgc-m947), javítva a `main@38987c8`-ban.
- **A schema source assertion `metadata.sign` mezőjét nem ellenőrzi kriptográfiailag a Relay**, és `source_assertion` nélkül is `asserted=true` az eredmény.
- **A `CIC_TRUST_MODE` kötelező, nincs alapértelmezése** (üres vagy hiányzó érték indulási hiba). A `dev` mód a binárisba ágyazott fejlesztői CA-tanúsítványokban bízik; a compose fájl alapból `dev`-et ad át.
- **A ProofTrace aláírás nélkül is elindul**, letiltott rögzítéssel, ha nincs `VAULT_TOKEN`.
- **A compose a host Docker-socketjét csatolja**, ami a host daemonjának jogosultságát adja a konténernek.
- **Az R6–R10 követelmények** (jóváhagyás, egyszeri felhasználás, bizonyíték) a Relay oldalán még nincsenek megvalósítva.

Ezek az állapotok az ismert vizsgálat időpontjában (2026-11-08, `main@1619a48`) igazak, a modulbetöltésre és a `git` host függvényekre vonatkozó pont 2026-11-15-i, `main@38987c8` szerinti állapot; a [`SECURITY.md`](SECURITY.md) írja le a hibabejelentés módját.

---

## Architektúra

```
cmd/relay        HTTP API (:8080), bootstrap, PKI asset betöltés
core/cabinet     Schema/Module/Workflow registry + WASM végrehajtó (wazero)
core/nexus       IaC loader, Git recorder, Vault crypto, sync, isolation, operator
```

A jelenlegi **futó runtime középpontja** a `cmd/relay` + `core/cabinet`.
A `core/nexus` komponensei közül a `recorder` és `crypto` aktívan be van kötve (ProofTrace aláírás).
Az `iac`, `operator`, `sync`, `isolation` részek implementáltak, de a fő futási útvonalba
még nincs minden bekötve — ezek az architektúra következő rétegei.

---

## Gyors start

```bash
# Infrastruktúra indítása (Docker-alapú builder)
make infra-init

# Tesztek
make test

# Golden tesztek (kanonikus formátum egyezés)
make verify

# Build
make build
```

### Fejlesztői Vault (opcionális, ProofTrace aláíráshoz)

```bash
make dev-vault                                        # lokális in-memory Vault
source $XDG_RUNTIME_DIR/cic-vault-dev.env            # env betöltése
make test                                             # tesztek Vault-tal
make dev-vault-stop                                   # leállítás
```

### E2E smoke tesztek

```bash
# Futó relay instance szükséges (pl. make build && ./output/<commit>/cic-relay)
RELAY_URL=http://localhost:8080 make test-api
```

---

## Dokumentáció

- [Roadmap](ai/ROADMAP.md)
- [Mérföldkövek](docs/hu/roadmap/relay_milestones.md)
- [Biztonsági modell](docs/hu/security/secret_and_audit_model.md)
- [Biztonsági irányelv és bejelentés](SECURITY.md)
- [Rendszer kontextus (AI)](ai/SYSTEM_CONTEXT.md)