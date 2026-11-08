# Biztonsági irányelv / Security policy

## Állapot

A CIC Relay jelenleg **referencia- és PoC-implementáció**. Nem hardened, és **nem production-használatra készült**: az ismert korlátokat a [README „Állapot és biztonsági helyzet"](README.md#állapot-és-biztonsági-helyzet) szakasza sorolja fel. Támogatott kiadási ág nincs; a javítások a `main` ágra kerülnek.

## Sérülékenység bejelentése

Kérjük, **ne nyisson nyilvános issue-t** biztonsági hibáról.

- Használja a GitHub **„Report a vulnerability"** funkcióját (a repó *Security* fülén, *Advisories*), vagy
- írjon a `sinkog@centralinfracore.hu` címre.

Kérjük, a bejelentés tartalmazza a hiba leírását, a reprodukálás lépéseit és az érintett commitot vagy verziót. A kulcsot, tokent vagy más titkot ne csatolja. Törekszünk az elfogadás visszaigazolására néhány munkanapon belül; ez nem szerződéses vállalás.

## Mi számít biztonsági hibának, és mi ismert korlát

Az ismert korlátok (a README-ben felsoroltak) nem számítanak új bejelentésnek, de a javításukra vonatkozó ötletet szívesen vesszük. A hatókörön kívül esik a saját telepítés konfigurációs hibája, és az a használat, amelyet a README kifejezetten ellenjavall (például a Relay HTTP API-jának hálózatra kitétele).

## English summary

CIC Relay is a **reference / proof-of-concept implementation**: it is not hardened and not meant for production use. Known limitations are listed in the README section "Állapot és biztonsági helyzet". Please do not report vulnerabilities in public issues: use GitHub's "Report a vulnerability" (Security → Advisories) or write to `sinkog@centralinfracore.hu`. There is no supported release branch; fixes land on `main`. We aim to acknowledge a report within a few working days; this is not a contractual commitment.
