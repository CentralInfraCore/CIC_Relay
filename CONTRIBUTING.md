# CONTRIBUTING

Köszi, hogy hozzájárulsz! Ez a repo **patch-first**, determinisztikus elvek szerint működik.

## Alapelv
- Gondolkodj **diffekben** (kis, ellenőrizhető patch-ek).
- A bíró: `make verify` (golden egyezés) + DoD pipa lista.

## DoD (Definition of Done)
- [ ] `make build` / cél build zöld (ha releváns)
- [ ] `make verify` zöld (GOLDEN egyezés)
- [ ] `context/CONTRACT.md` invariánsok betartva
- [ ] `context/SYMBOLS.md` szimbólumok/név-konvenciók betartva
- [ ] `context/LIMITS.md` keretek nem sérülnek (ha releváns)
- [ ] Golden változás esetén rövid indoklás a PR leírásban

## Golden szabályok
- Kimenet **determinista** (kulcsrendezés, locale/clock/RNG nincs).
- Sorvégi newline legyen.
- Bemenet/kimenet minimális, de **reprezentatív**.

## Commit / PR
- Kompakt commitok; commit üzenet: *scope: rövid leírás*.
- PR sablon DoD pipákkal (`.github/pull_request_template.md`).
- Ha szerződéshez (CONTRACT/SYMBOLS) nyúlsz, **bumpold az `llm.lock`-ot** (lásd `ai/LLM_LOCK.md`).

## Lokális minőségkapu
- `scripts/quality.sh` – fmt → lint → build → verify (safe fallbackekkel).
- Formázáshoz: `FMT_WRITE=1 scripts/fmt_local.sh`.
