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
| A5 | Futó példány (`git` és `cic-flow` egyaránt) ugyanazon modulazonosító más manifesttel vagy más artifacttal való újratöltése, sikertelen újratöltés és a grant visszavonása után | a korábbi példány minden esetben DENY; a változatlan kötésű, aktív grantű példány ALLOW a szabályokon belül |
| A6 | A rekord a WASM-ot fedi, a leírót nem (vagy fordítva) | elutasítva (4.1) |
| A7 | `release.sign` érvényes, a `cic_countersign` hiányzik, hibás, vagy nem a CICSourceCA-ig vezető tanúsítványtól van | `prod`-ban elutasítva; `NOT_RUN` / `FAIL`-ként jelentve, soha nem átmenetként |
| A8 | Envelope 1 vagy 2 rekord | elutasítva, `envelope_too_old`; nem frissítve |
| A9 | Érvényes aláíró, a policy szerint nem erre a modulra jogosult | DENY |
| A10 | Érvényes aláíró, a policynál szélesebb capability-készlet | DENY |
| A11 | Módosított manifest, a lokális digest hozzáigazítva | DENY (az aláírt rekordbeli digest nem egyezik) |
| A12 | A fájl lemezen kicserélve az ellenőrzés után (a fájlrendszer tesztdublőr, amely az első olvasás után más bájtokat ad) | a végrehajtott artifact és a grant az ellenőrzött bájtokból készül; a kicserélt bájtok sosem futnak; minden fájl egyszer olvasva |
| A13 | Újratöltés előtt létrehozott `cic-flow` példány, ugyanazon modulazonosítóra adott új grant mellett | DENY (a példány nem az az artifact, amelyhez az új grant tartozik) |
| A14 | Staged `cic-flow` grant az aktiválás előtt | DENY |
| A15 | Hibás rekord `dev` opt-in mellett | DENY; az opt-in nem kerüli meg az ellenőrzést, és nem ad hitelesített grantet |
| A16 | Az artifact tárolása az ellenőrzés után sikertelen | a grant nem aktiválódik |
| A17 | A -> B -> A életciklus: A betöltése, B betöltése, A újratöltése | a B alatt DENY példányt az A identitásának visszaállítása nem engedélyezi újra; csak az A újratöltése után létrehozott példány engedélyezett |
| A18 | Olyan rekord, amelynek útvonalai nem pontosan felelnek meg a betöltött fájloknak (csak névegyezés) | elutasítva; fájlnév-alapú fallback nincs |
| A19 | A valódi compiler-rekord (`primitives/@v0.2.1`, a származás rögzítve) a Go verifieren | `record` és `crypto` PASS, ugyanaz, mint a Python referencia-verifier. A modulszintű valódi compiler-rekord **blokkolt**, amíg a modul release-tooling nem tud envelope 3 rekordot kiadni (terv 3. szakasz); ez producer-munkatétel, nem kihagyott teszt |
| A20 | Ugyanazon artifact újratöltése, miután a korábbi példány DENY lett (visszavonás vagy sikertelen újratöltés) | új grant-generáció jön létre; a korábbi példány DENY marad, csak az új generáció alatt létrehozott példányok ALLOW |
| A21 | Csak a modul saját kiadója által aláírt policy | DENY (nincs önjogosítás) |
| A22 | Modulkiadási joggal rendelkező, de policy-kiadási joggal nem rendelkező aláíró policyt ír alá | DENY |
| A23 | A policy hiányzik, érvénytelen, vagy nem alkalmazható a modulra | DENY |
| A24 | A policy digestje | a granttel és az audit naplóban rögzítve; megváltozott policy megváltoztatja a digestet, és új döntést igényel |

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
