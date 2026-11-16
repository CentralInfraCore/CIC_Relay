# Modul-artifact hitelesítés (#162): tesztterv

Státusz: **concept.** Az [artifact-authentication.md](artifact-authentication.md) társdokumentuma; a szakaszszámok azé. Semmi nincs megvalósítva; minden alábbi tesztet úgy kell megírni, hogy a jelenlegi `main`-en **elbukjon**, mielőtt a megvalósítás létezik.

## 1. Elfogadás (negatív tesztek)

Minden teszt valódi modult és valódi, teszt-kulcsos release rekordot épít (kulcsok rögzített címkékből levezetve, mint a primitives vektoroknál), és `prod`-ban mindkét betöltőn (embedded, installed) átviszi.

| # | Teszt | Elvárás |
|---|---|---|
| A1 | Érvényes tanúsítvány (a trust store-ig és a CICSourceCA horgonyig vezet) egy másik által épített modul mellé másolva, rekord nélkül vagy olyan rekorddal, amely nem fedi ezt a WASM-ot | elutasítva; capability nincs |
| A2 | A `.wasm` egyetlen bájtja módosítva az aláírás után | elutasítva (`shipped`: digest-eltérés) |
| A3 | A `capability_manifest` egyetlen jogosultsága módosítva az aláírás után (szélesebb egress, extra secrets bejegyzés, extra git szabály) | elutasítva |
| A4 | Azonos modulnévvel és verzióval betöltött másik artifact | elutasítva, vagy a korábbi grant nélkül töltődik be; a korábbi modul capabilityjét nem örökli |
| A5 | Futó példány ugyanazon modulazonosító más manifesttel való újratöltése után | megtartja a létrehozáskor kapott kötést; sem szélesedés, sem szűkülés |
| A6 | A rekord a WASM-ot fedi, a leírót nem (vagy fordítva) | elutasítva (4.1) |
| A7 | `release.sign` érvényes, a `cic_countersign` hiányzik, hibás, vagy nem a CICSourceCA-ig vezető tanúsítványtól van | `prod`-ban elutasítva; `NOT_RUN` / `FAIL`-ként jelentve, soha nem átmenetként |
| A8 | Envelope 1 vagy 2 rekord | elutasítva, `envelope_too_old`; nem frissítve |

## 2. Fail-closed szabálytáblázat

A `module_signing_test.go` döntési táblája bővül: modulok (nincs rekord, érvényes rekord, minden érvénytelen rekordfajta) x környezetek (`prod`, `prod`+opt-in, `dev`, `dev`+opt-in) x betöltők. Kötelező: semmilyen opt-in nem enged be érvénytelen rekordot; `prod` soha nem tölt be ellenőrizhető rekord nélküli modult (D3); a `dev` opt-in csak a „nincs rekord" esetet engedi.

## 3. Verifier-megfelelőség

* A primitives golden és negatív vektorok, a származásukkal (commit, SHA-256) együtt átmásolva, a Go verifieren átfutnak: a szerződés 11. szakaszának minden okához van legalább egy vektor, és ugyanaz az ok jelenik meg.
* Az aláírás-lefedettségi sorozat: a sorozat minden hibája azon a rétegen bukik, amelyet a primitives dokumentum megnevez.
* Differenciális: generált rekordok (érvényes és mutált) a Go verifierrel és a `tools/release_record_conformance.py`-vel; bármely ítélet- vagy okeltérés bukik.
* Planted defect: a Go verifier minden ellenőrzését sorban kikapcsoljuk; mindegyikre egy vektornak buknia kell.

## 4. Életciklus és audit

* A grant kulcsa tartalmazza a `build_hash`-t, a wasm digestet, a manifest digestet és az aláíró ujjlenyomatot (4.4); komponensenként unit teszt.
* A betöltési napló rögzíti az ellenőrzött `build_hash`-t és az aláíró ujjlenyomatot (audit-híd), az elutasítás pedig a réteget és az okot.
* Ugyanazon modulazonosító párhuzamos betöltése (a meglévő `moduleLoadMu` tesztek) az új kulccsal is érvényes marad.

## 5. A megvalósító PR-hoz kötelező bizonyíték

Mutációs bizonyíték (killed / equivalent / survived-then-strengthened) a verifierre, a szabályra és a kötési kódra; a #197-es CI-egyenértékű futás (gofmt -s, vet, staticcheck, govulncheck, deadcode, golden verify, symbol/manifest drift, `-race -count=2 -shuffle=on`, lefedettségi küszöbök); az aláírt commitlánc; EN/HU dokumentáció. A D1–D7 döntések a tervben dőlnek el, mielőtt kód készül.

## 6. Itt nem tervezett

A WASM importlista-ellenőrzés, a `git.push` újra bevezetése és az Oracle végponttól végpontig teszt saját tervet kap.
