![verify](https://github.com/<ORG>/<REPO>/actions/workflows/verify.yml/badge.svg) ![manifest-check](https://github.com/<ORG>/<REPO>/actions/workflows/manifest-check.yml/badge.svg)

# CIC — Patch‑First, Deterministic Repo (AI Quickstart)

> Gondolkodj diffekben, ne oldalakban. Ez a repo **nem kér magyarázatot**, csak **ellenőrizhető eredményt**.

## 1‑perces gyorsstart
1. Olvasd el `ai/activation.txt` (8 sor).
2. Nézd meg a `context/CONTRACT.md`, `context/SYMBOLS.md`, `context/TESTING.md` fájlokat.
3. A `GOLDEN/*.in` → `*.out` párok adják a siker mércéjét.
4. Implementálj úgy, hogy `make verify` zöld.

## Definition of Done (DoD)
- `make fmt && make lint` zöld
- `make build` / cél szerinti build zöld
- `make verify` zöld (golden egyezés)
- Invariánsok tartva (`context/CONTRACT.md`)
- Szimbólumok egyeznek (`context/SYMBOLS.md`)

## Canonicalizálás (példa feladat)
- Input: JSON (stdin)
- Output: **kanonikus, rendezett, kompakt JSON** (stdout, végén newline)
- Mellékhatás: nincs (clock/locale/RNG tiltott; mindent injektálunk)

## Patch‑módszer
- Kis, ellenőrizhető diffek
- Golden változás → rövid indoklás a PR sablonban
- Ha eltérés kell a szerződéshez képest: **szerződés‑diffet** javasolj, ne térj el némán
