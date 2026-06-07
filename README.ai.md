![verify](https://github.com/CentralInfraCore/CIC_Relay/actions/workflows/verify.yml/badge.svg) ![symbols](https://github.com/CentralInfraCore/CIC_Relay/actions/workflows/symbols.yml/badge.svg)

# CIC-Relay — AI Quickstart

> Gondolkodj diffekben, ne oldalakban. Ez a repo **nem kér magyarázatot**, csak **ellenőrizhető eredményt**.

## 1 perces gyorsstart

1. Olvasd el `ai/activation.txt` (8 sor).
2. Olvasd el `ai/SYSTEM_CONTEXT.md` — mi implementált, mi scaffold.
3. Nézd meg `context/CONTRACT.md`, `context/SYMBOLS.md`, `context/LIMITS.md`.
4. A `GOLDEN/*.in` → `*.out` párok adják a determinizmus mércéjét.
5. Implementálj úgy, hogy `make test && make verify` zöld.

## Definition of Done (DoD)

- `make test` zöld (unit + integráció)
- `make verify` zöld (golden fájl egyezés)
- `make check-symbols` zöld (SYMBOLS.md drift = 0)
- Invariánsok tartva (`context/CONTRACT.md`)
- HTTP hibák: `writeError` helper, nem `http.Error`

## Architekturális igazságok

| Állítás | Valóság |
|---|---|
| Entrypoint | `cmd/relay/main.go` (nincs `cmd/nexus/main.go`) |
| Futó runtime | `cmd/relay` + `core/cabinet` |
| Nexus bekötés | `recorder` + `crypto` aktív; `iac`, `operator`, `sync`, `isolation` implementált de nem bekötve |
| PKI enforcement | `pki_verify.go` létezik, de scaffold — CA lánc nem bootstrapped |
| ProofTrace | Implementált — per-step hash, chain_hash, opcionális Vault aláírás |

## Patch-módszer

- Kis, ellenőrizhető diffek
- Ha scaffold-ot látsz: ellenőrizd `ai/SYSTEM_CONTEXT.md`-ben hogy szándékos-e
- Golden változás → rövid indoklás a PR sablonban
- Ha eltérés kell a szerződéshez képest: **szerződés-diffet** javasolj, ne térj el némán
