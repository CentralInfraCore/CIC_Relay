# CIC Relay

A CIC-Relay egy **ProofTrace-alapú, auditálható végrehajtási platform**.
Minden végrehajtott workflow determinisztikus hash-láncot alkot,
amelynek integritása kriptográfiailag igazolható.

A bizalom nem feltételezés — igazolható állapot.

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
- [Rendszer kontextus (AI)](ai/SYSTEM_CONTEXT.md)