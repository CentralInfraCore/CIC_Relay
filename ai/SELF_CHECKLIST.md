# Self Checklist

## Alapkövetelmények
- [ ] Betartottam a CONTRACT invariánsait (`context/CONTRACT.md`)
- [ ] SYMBOLS-szal egyezik a név és signature (`context/SYMBOLS.md`)
- [ ] Golden egyezés (`make verify`)
- [ ] Teljesítmény/memória kereten belül (`context/LIMITS.md`)
- [ ] Változás diff + indoklás leírva

## Determinizmus
- [ ] Nem vezetek be clock/locale/RNG mellékhatást
- [ ] Ha hash-elésbe kerülő dolgot módosítottam: GOLDEN tesztek futtatva
- [ ] `CanonicalizeSetPayload` szemantikája megőrzött (dedup+sort)
- [ ] Új struktúra esetén: csak stabil mezők kerülnek checksum-ba

## Trust ecosystem tudatosság
- [ ] Ha placeholder-t látok: megvizsgáltam, hogy trust ecosystem hiányából fakad-e
- [ ] Nem kötöttem be PKI/CA logikát ott ahol az előfeltétel (CA lánc) még nem áll fenn
- [ ] A `"outcome": "skipped"` log helyett nem írtam `"success"`-t nem ellenőrzött kódútban
- [ ] `UpstreamSource`, `graph.go`, `LocalWorker` in-process jellegét nem "javítottam"

## ProofTrace integritás
- [ ] Ha Setx végrehajtást érintek: a hash lánc megőrzött vagy tervszerűen bővített
- [ ] Új workflow lépés esetén: az input/output hash-elhető és determinisztikus
- [ ] A recorder összekötés szemantikája nem sérül

## Kód minőség
- [ ] Interface-ek mögé bújtatott logika (nem direkten a struct-on)
- [ ] Új függőség esetén: mock interfész is elkészült
- [ ] Sentinel hibák használata (`ErrInvalid`, `ErrNotFound`, `ErrConflict`)
- [ ] Context propagálás végig (nem `context.Background()` belső hívásokban)