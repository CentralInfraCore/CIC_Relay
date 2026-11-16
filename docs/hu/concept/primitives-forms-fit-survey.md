# Primitives és Forms illeszkedési és hiányossági felmérés a release rekordhoz és az authorization policyhoz (#162)

Státusz: **concept (felmérés).** Olvas és mér; nem ad hozzá sémát, grammatikát, kódot vagy validációs architektúrát, és sem a cic-primitives-t, sem a Formst nem módosítja. A [module-release-record-plan.md](module-release-record-plan.md) (#211-ben mergelve) folytatása, és arra felel, amit az a terv nyitva hagyott: a release rekordból és az authorization policyból mennyit fed már a meglévő CIC gépezet, és mi hiányzik ténylegesen. A Relay implementáció, a base-repo kibocsátó, az Oracle végponttól végpontig teszt és a produkciós jóváhagyás HOLD.

## 1. A felmérés szabálya

Nincs második Form, nincs második validációs rendszer, nincs párhuzamos grammatika. Hiányzó **általános strukturális** képesség a Forms ügye, nem a Relayé vagy egy release compileré. Egy lelet csak akkor hiányosság, ha semmilyen meglévő komponens nem fedi; a „létezik, de erre a dokumentumfajtára nincs alkalmazva” más sor.

## 2. Források és mi futott le

Friss, sekély klónok, ha nincs mást írva `main`: `cic-primitive-forms` `2f02a75`, `cic-primitive-engine` `a6a0680`, `cic-schema-registry` `ac567ae`, `cic-primitives` `devel` `01cb30b` és a `primitives/@v0.2.1` tag.

Lefuttatva: a primitives referencia-verifier `tools/release_record_conformance.py` a `v0.2.1` fa két release rekordján; vegyes skalárok PyYAML-os beolvasása; `openssl` a `v0.2.1` rekordban lévő tanúsítványokon; kulcsszókeresés a négy repóban. **Nem futott:** Forms build vagy teszt, bármely rekordséma `compile_schema`-ja, engine build. A Forms kifejezőképességéről szóló állítások dokumentumból olvasottak, és így vannak jelölve.

## 3. A komponenshatárok, ahogy találtam

| Komponens | Mit mondanak róla a dokumentumok | Alkalmazva van-e ma release rekordra vagy policyra? |
|---|---|---|
| cic-primitives | Atomok és aggregátumok, az `access-policy/1` hozzáférési grammatika, a compiler, a release rekord szerződés (envelope 3), a referencia-verifier | A rekord-szerződés és a verifier: igen. A `release.schema.yaml` rekordséma **JSON Schema**, és csak a `PrimitiveRelease`-re szól. |
| cic-schema-registry | Konszolidált sématár | Release rekord- vagy policy-sémát nem találtam benne. |
| Forms | Parse, emit, pinelt CIC séma elleni validáció, expand/collapse, normalize a származás megtartásával, az effektív access/modify szabály adatként (`PolicyResolution`), diff, patch, a P5 nyers-YAML, admission, trust-model és release-origin szerződések | Release rekordra nincs alkalmazva. A saját README szerint: „nincs jogosultsági döntés”. |
| Primitive Engine | Materializálás, conformance-ítélet, **materializált objektum** kanonikus bájtjai és digestjei | Nem. A digestjei materializált objektumok szemantikai digestjei, nem a release `build_hash`. |
| Release verifier (primitives) | `record`, `blobs`, `crypto` rétegek; ok-azonosítók | Igen. A trust gate-ek kifejezetten kívül esnek rajta. |
| CICSourceCA szolgáltatás | A `build_hash`-t ellenjegyzi; a `workflow` a hívó adta szöveg, nem allowlistelt | Ma **nem** jogosultsághoz kötött (lásd a terv 6. szakaszát). |
| Relay | Modulokat tölt; bizalmi döntést hoz, capabilityt oszt, végrehajt | CertPattern-illesztője és rekord-verifiere még nincs. |

## 4. Hiányossági elemzés

Státuszszavak: **implemented**, **scaffold**, **concept**, **gap** (semmi nem fedi).

| # | Szükséges | Mi létezik | Státusz | Lelet |
|---|---|---|---|---|
| G1 | Minden rekordfajtára közös hash- és ellenőrzési szabály | A `_release_hash_payload` envelope 2 és 3 esetén csak a `release.sign`, `release.build_hash` és `cic_countersign` tagot zárja ki; a `kind`-ot és a `specs`-et nem nézi. A verifier az `envelope`, `source_commit`, `artifacts` mezőt olvassa. | implemented | A mechanizmus már fajtafüggetlen. Nincs mit építeni. |
| G2 | A rekordséma közös része és fajtánként egy séma | A `release.schema.yaml` `kind` const `PrimitiveRelease`-t és nem üres `specs`-et követel; az envelope 3 feltételek benne ismétlődnek. | gap | A sémát közös és fajtánkénti részre kell bontani a cic-primitives-ben. Ez a review által kért szerződésváltozás. |
| G3 | A rekordszerkezet leírása a CIC séma nyelvén (Forms Shape-ek) | A Forms pinelt CIC séma ellen validál: `string`, `integer`, `boolean`, lista `min_items`-szel, kompozit `fields`, opaque; mandatory/optional/nullable; `UNKNOWN_FIELD`; listaelem-identitás deklarált kulccsal; pozíció és útvonal minden hibán. | concept (olvasott, nem futtatott) | A szerkezet (beágyazás, kötelező tagok, ismeretlen tagok, az `artifacts.files` listaelem-identitása `path` szerint) kifejezhetőnek látszik. Az értékkorlátok nem: `kind` const, `envelope` egyenlő 3, 40 hexa `source_commit`, `sha256:` alak, rendezettség, manifest-konzisztencia. A támogatott részhalmazban nem találtam pattern-t, enumot vagy constot. Ezek az ellenőrzések a verifier ellenőrzési táblájában vannak, és ott is maradnak. **Forms-változtatást nem javaslok**; a tulajdonosi szabály szerint Forms-módosításhoz igazolt hiányosság kell, és ilyet nem igazoltam. Hogy a `compile_schema` elfogad-e ilyen sémát, nem próbáltam. |
| G4 | A hitelesített bájtokat olvassuk | A Forms P5 admission szerződés: csak akkor admitálunk, ha a normatív tábla engedi, az általunk használt olvasók azonosan olvassák, és a provenance igazolt; az olvasó a hitelesített bájtokon fut (D8 döntés). | concept (szerződés), release-hez nincs kötve | Közvetlenül alkalmazható. Ez a helyes bejárat a Relay rekordolvasásához. |
| G5 | Zárt értéktartomány a rekordhoz | A `v0.2.1` rekord csak `str`, `dict`, `list`, `bool`, `None` és `int` értéket tartalmaz (mérve: 842, 333, 72, 34, 1, 6); az időbélyegek és érvényességi dátumok idézettek. A szerződés nem mond értéktartományt. | gap (kicsi) | Lásd az 5. szakaszt: a tartomány megvalósítható, de nincs kimondva, és a hash-payload `default=str`-t használ, ami elrejtene egy típusos skalárt. |
| G6 | Listaelemek identitása | A Forms `PathOf` a listaelemet deklarált kulcsa szerint nevezi meg; a verifier ma maga ellenőrzi az útvonal-duplikációt. | implemented (Forms), duplikáció-ellenőrzés implemented (verifier) | Új képesség nem kell. |
| G7 | Normalizálás és alapértékek | A Forms `normalize` minden érték származását megtartja, a `denormalize` visszaadja a szerzői dokumentumot. | implemented | Csak olvasási nézetként használható. A normalizált dokumentum soha nem helyettesítheti az eredetileg aláírt bájtokat: a digest és az aláírás mindig a szerzői bájtokon van. |
| G8 | Effektív policy-feloldás | A `PolicyResolution` pozíciók effektív `access`/`modify` szabályát adja adatként. A terve kimondja: nincs allow vagy deny, nincs CertPattern-illesztés, nincs hitelesítés. | implemented, más célra | Kezelt objektumok mezőinek szabályát oldja fel. Nem release-jogosultság feloldó, és nem dönt. A döntést a Relay hozza. |
| G9 | Policy-család arra, ki írhat alá és ellenjegyezhet | A Forms `P5-TRUST-MODEL-PLAN` 7. szakasza már definiál egy, a rekordon és az aláírók hatókörén kívül tartott policyt, sorokkal: horgonyok, release-aláírók, ellenjegyzők, mindkettő-kötelező és különböző-identitás szabály, időbizonyíték, visszavonás, algoritmusok. T1–T8 tulajdonosi döntések. | plan (concept) | A terv `ReleaseAuthorizationPolicy`-ja nem lehet új, párhuzamos dokumentum. Ennek a trust policynak a modul-hatókörű kiterjesztése. Csak a modul-hatókör és a capability-plafonok új. |
| G10 | `capability_manifest` séma | A kulcsszó a négy repó egyikében sincs meg. Csak a Relayben és a modulok `project.yaml`-jában létezik. | gap | Plafon nem írható és nem validálható CIC séma ellen, amíg a capability manifestnek nincs sémája. Ez típusspecifikus séma (registry), nem Form-képesség. |
| G11 | Tanúsítvány illesztése CertPattern-re | Az `IsCertPattern` csak szintaxist ellenőriz. A Relayben, a Formsban, a primitives-ben és az engine-ben nincs illesztő. | gap | Új kód, a Relay munkacsomagban. Nem Form-képesség (hitelesített tanúsítvány kell hozzá, amit a Forms nem tarthat). |
| G12 | A policy digestje a grant mellé | Az engine materializált objektumokat digestel (szemantikai). A release rekord nyers artifact-bájtokat digestel. | két digest-világ | A grantbe kötött policy-digest a **hitelesített policy-bájtok** digestje (az `artifacts` bejegyzése). Az engine szemantikai digestje kiegészítő mező lehet, helyettesítő soha. |
| G13 | Idő és lánc-érvényesség | A Forms trust terv 1–5. szakasza: a mostani lánc minden tanúsítványa 2026-12-31-én lejár; a verifier 2027-01-01-től elutasítja a láncot; a lejárat előtti létezés bizonyítékát a lejárat előtt kell létrehozni; az ellenjegyzett payload a konstans `build_hash`, tehát nincs benne idő. | concept (terv), **határidő** | Itt újramérve a `v0.2.1` rekordból: fejlesztői tanúsítvány `notAfter` 2026-12-31 13:22:59 GMT, Source CA 2026-12-31 13:22:39 GMT. Ma 2026-11-16, **45 nap** van hátra. Lásd a 6. szakaszt. |
| G14 | A régi rekordok ellenőrizhetősége megmarad | A referencia-verifier az envelope 1 rekordot (`v0.1.5`) `envelope_too_old` okkal elutasítja, szerződés szerint; a `v0.2.1` rekord átmegy a `record` és `crypto` rétegen. A `blobs` réteg `NOT_RUN` volt (a forrás-commit nincs a sekély klónban). A `v0.2.0` rekord nincs a `v0.2.1` fában, nem futott. | implemented | A „változatlan ellenőrizhetőség” azt jelenti: az envelope 1 és 2 a compiler saját útján marad ellenőrizve, az envelope 3 bájtjai és a verifier ítéletei azonosak maradnak. Az általánosításhoz minden meglévő rekordra kell egy golden vektor. |

## 5. A parser-határ, mérve (G4, G5)

A rekord hash-e egy Python értékfa felett készül (`json.dumps(sort_keys=True, separators=(",",":"), default=str)` azon, amit a PyYAML adott). A Forms szándékosan szűkebb YAML-részhalmazt olvas: minden plain skalárt, a `null`, `true`, `false` és a 64 bites decimális egészek kivételével, szövegnek vesz, a lebegőpontosat, hexát, oktálist és a vezető nullás egészet pedig elutasítja (`UNSUPPORTED_SYNTAX`). A két olvasat eltér. Egy minta, PyYAML `safe_load`-dal beolvasva és úgy kanonizálva, ahogy a compiler teszi:

| Bemeneti skalár | PyYAML, majd kanonikus JSON | Forms |
|---|---|---|
| `1.0` | szám `1.0` | elutasítva (`UNSUPPORTED_SYNTAX`) |
| `0x10` | szám `16` | elutasítva |
| `1_000` | szám `1000` | szöveg |
| `yes` | `true` | a `yes` szöveg |
| `2026-11-02T18:57:58+00:00` idézőjel nélkül | a `2026-11-02 18:57:58+00:00` szöveg (szóközzel, a `default=str` miatt) | a `T`-s szöveg |
| `1e3` | az `1e3` szöveg | elutasítva |

Egy idézőjel nélküli időbélyeg vagy egy `yes` tehát más kanonikus bájtot adna a PyYAML-ban és egy szigorú olvasóban, és a `default=str` egy típusos dátumból olyan szöveget csinál, amely sosem volt a fájlban. A publikált rekord idézéssel kerüli el ezt, és a mért értéktartomány zárt, így a probléma valós, de behatárolt. A javaslat ezért kicsi, és a szerződéshez és a verifierhez tartozik, nem a Formshoz:

* A rekord-szerződés kimondja az értéktartományt (string, integer, boolean, null, lista, mapping), és hogy minden más skalártípust a verifier hash-elés előtt elutasít.
* A kibocsátó idézett stringeket ír.
* A Relay rekord- és policy-olvasója a hitelesített bájtokon fut a Forms admission szerződése szerint; hogy a Relay a Forms Go könyvtárát importálja-e, vagy ugyanazt az admitált részhalmazt valósítja meg, a Relay munkacsomag döntése.

## 6. Ütemezési tény az Oracle PoC-hoz (G13)

A meglévő lánc mindkét aláíró tanúsítványa 2026-12-31-én lejár (fent mérve). Az alatta aláírt rekordot az „most” időpontban érvényességet ellenőrző verifier 2027-01-01-től nem fogadja el. A Relay terve (D5) betöltéskor ellenőrzi az érvényességet, így minden ezen lánccal aláírt modul 2027-01-01-től nem töltődik be, hacsak a láncot nem újítják meg, vagy meg nem őrzött időbizonyítékkal as-of szabályt nem fogadnak el. A trust terv kimondja, hogy a lejárat előtti létezés bizonyítéka a lejárat után nem hozható létre. Ezek a terv T3, T4 és T6 tulajdonosi döntései. A #162-re ez azt jelenti: az Oracle PoC-nak és minden tesztrekordnak olyan lánccal kell aláírva lennie, amely túléli a tesztet, vagy teszt-tanúsítványokat használnak (a terv a vektorokhoz már teszt-kulcsos rekordokat használ). Ütemezési korlát, nem tervváltozás.

## 7. Mit javasol ez a felmérés

1. **Szerződés-általánosítás (cic-primitives, a következő megvalósítási PR, amint engedélyezett):** közös envelope rész (a szerződés 4. szakaszának tagjai, a hash-szabály, a verifier rétegei), kimondott értéktartomány (5. szakasz), fajtánkénti sémák (`PrimitiveRelease`, `ModuleRelease` és a policy-fajta), és minden meglévő rekordra egy golden vektor, amely bizonyítja, hogy az ítélete nem mozdul. A hash-mechanizmuson nem változtatunk.
2. **Forms-változtatás nincs**, mert általános strukturális hiányt nem igazoltam. A Formst ott használjuk, ahol már illeszkedik: az admission bejárat (G4), a listaidentitás (G6) és az olvasási normalizált nézet (G7).
3. **A D4 a Forms 7. szakasza trust policyjának kiterjesztése** (G9), nem új dokumentumcsalád: modul-hatókör és capability-plafonok hozzáadása; a horgonyok, aláírók, ellenjegyzők, mindkettő-kötelező és idő sorok változatlanok. A döntést a Relay hozza; a Forms csak azt oldja fel, ami adat.
4. **Registry:** séma a `capability_manifest`-hez (G10), mielőtt bármilyen plafon validálható.
5. **Relay munkacsomag** (még HOLD): a CertPattern-illesztő (G11), a rekordolvasó admitált bájtokon, a grantbe kötött policy-digest a hitelesített policy-bájtokból (G12).
6. **Most felvetendő tulajdonosi döntések:** T3 (időbizonyíték a mostani láncra 2026-12-31 13:22 UTC előtt), és hogy a Relay importálja-e a Formst.

## 8. Nem igazolt

Forms- vagy engine-tesztet nem futtattam. Azt, hogy a `compile_schema` elfogad-e rekordsémát, nem próbáltam. Hogy a `PolicyResolution` alkalmazható-e policy-dokumentumra a meglévő atomokkal, nem próbáltam; objektummezőkre tervezettként olvastam. A `v0.2.0` rekordot és a `blobs` réteget a valódi forrás-commiton nem futtattam. A négy repón kívül nem kerestem `capability_manifest` sémát. A `capability_manifest` előfordulásait a Relayben és az Oracle modulban itt nem mértem újra. A Forms trust terv 2026-11-02-i; csak a két tanúsítvány-dátumot mértem újra.
