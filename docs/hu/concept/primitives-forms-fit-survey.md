# Primitives és Forms illeszkedési és hiányossági felmérés a release rekordhoz és az authorization policyhoz (#162)

Státusz: **concept (felmérés).** Olvas és mér; nem ad hozzá sémát, grammatikát, kódot vagy validációs architektúrát, és sem a cic-primitives-t, sem a Formst nem módosítja. A [module-release-record-plan.md](module-release-record-plan.md) (#211-ben mergelve) folytatása, és arra felel, amit az a terv nyitva hagyott: a release rekordból és az authorization policyból mennyit fed már a meglévő CIC gépezet, és mi hiányzik ténylegesen. A Relay implementáció, a base-repo kibocsátó, az Oracle végponttól végpontig teszt és a produkciós jóváhagyás HOLD.

## 1. A felmérés szabálya

Nincs második Form, nincs második validációs rendszer, nincs párhuzamos grammatika. Hiányzó **általános strukturális** képesség a Forms ügye, nem a Relayé vagy egy release compileré. Egy lelet csak akkor hiányosság, ha semmilyen meglévő komponens nem fedi; a „létezik, de erre a dokumentumfajtára nincs alkalmazva” más sor.

## 2. Források és mi futott le

Friss, sekély klónok, ha nincs mást írva `main`: `cic-primitive-forms` `2f02a75`, `cic-primitive-engine` `a6a0680`, `cic-schema-registry` `ac567ae`, `cic-primitives` `devel` `01cb30b` és a `primitives/@v0.2.1` tag.

Lefuttatva: a primitives referencia-verifier `tools/release_record_conformance.py` a `v0.2.1` fa két release rekordján; vegyes skalárok PyYAML-os beolvasása; `openssl` a `v0.2.1` rekordban lévő tanúsítványokon; kulcsszókeresés a négy repóban. Szintén lefutott (review F3): az 5.1 szakasz Forms illeszkedési próbája, egy reprezentatív `ModuleRelease` séma a Forms Rust és Go könyvtárán át (`docs/evidence/forms-fit-probe/`). **Nem futott:** a Forms tesztcsomagjai, engine build, a próbáén kívüli rekordséma. A Forms kifejezőképességéről a próbán túl szóló állítások dokumentumból olvasottak, és így vannak jelölve.

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
| G3 | A rekordszerkezet leírása a CIC séma nyelvén (Forms Shape-ek) | Egy reprezentatív `ModuleRelease` séma `compile_schema`-val lefordítva és tíz dokumentum validálva a Forms Rust és Go könyvtárával (a próba, 5. szakasz). | **mért** (próba) | A szerkezet kifejezhető, és a két implementáció mind a tíz esetben bájtra azonos eredményt ad. Az értékkorlátok nem kifejezhetők (`kind` const, `envelope` egyenlő 3, 40 hexa `source_commit`, `sha256:` alak, rendezettség, manifest-konzisztencia): a támogatott részhalmazban nincs ilyen, és ezek az ellenőrzések a verifierben maradnak. **Forms-változtatást nem javaslok**; egyet sem igazoltam szükségesnek. A próba architekturális bizonyíték, nem produkciós implementáció, és a fejlesztői grammatika-pin alatt futott (5. szakasz). |
| G4 | A hitelesített bájtokat olvassuk | A Forms P5 admission szerződés: csak akkor admitálunk, ha a normatív tábla engedi, az általunk használt olvasók azonosan olvassák, és a provenance igazolt; az olvasó a hitelesített bájtokon fut (D8 döntés). | concept (szerződés), release-hez nincs kötve | Közvetlenül alkalmazható. Ez a helyes bejárat a Relay rekordolvasásához. |
| G5 | A rekordot minden érdemi olvasó azonosan olvassa | A `v0.2.1` rekord csak `str`, `dict`, `list`, `bool`, `None` és `int` értéket tartalmaz (842, 333, 72, 34, 1, 6), és idézi az időbélyegeit és dátumait. A szerződés nem mondja ki, hogyan admitálunk rekordot, ha az olvasók eltérnek. | gap | A zárt értéktartomány nem zárja le (5.2): a rekord rétegnek meg kell követelnie az olvasók értelmezési azonosságát. |
| G6 | Listaelemek identitása | A Forms `PathOf` a listaelemet deklarált kulcsa szerint nevezi meg; a verifier ma maga ellenőrzi az útvonal-duplikációt. | implemented (Forms), duplikáció-ellenőrzés implemented (verifier) | Új képesség nem kell. |
| G7 | Normalizálás és alapértékek | A Forms `normalize` minden érték származását megtartja, a `denormalize` visszaadja a szerzői dokumentumot. | implemented | Csak olvasási nézetként használható. Soha nem helyettesítheti azokat a bájtokat vagy azt a fát, amelyeket egy digest vagy aláírás fed. A 6. szakasz szétválasztja a három digest-réteget. |
| G8 | Effektív policy-feloldás | A `PolicyResolution` pozíciók effektív `access`/`modify` szabályát adja adatként. A terve kimondja: nincs allow vagy deny, nincs CertPattern-illesztés, nincs hitelesítés. | implemented, más célra | Kezelt objektumok mezőinek szabályát oldja fel. Nem release-jogosultság feloldó, és nem dönt. A döntést a Relay hozza. |
| G9 | Policy-család arra, ki írhat alá és ellenjegyezhet | A Forms `P5-TRUST-MODEL-PLAN` 7. szakasza már definiál egy, a rekordon és az aláírók hatókörén kívül tartott policyt, sorokkal: horgonyok, release-aláírók, ellenjegyzők, mindkettő-kötelező és különböző-identitás szabály, időbizonyíték, visszavonás, algoritmusok. T1–T8 tulajdonosi döntések. | plan (concept) | A terv `ReleaseAuthorizationPolicy`-ja nem lehet új, párhuzamos dokumentum. Ennek a trust policynak a modul-hatókörű kiterjesztése. Csak a modul-hatókör és a capability-plafonok új. |
| G10 | `capability_manifest` séma | A kulcsszó a négy repó egyikében sincs meg. Csak a Relayben és a modulok `project.yaml`-jában létezik. | gap | Plafon nem írható és nem validálható CIC séma ellen, amíg a capability manifestnek nincs sémája. Ez típusspecifikus séma (registry), nem Form-képesség. |
| G11 | Tanúsítvány illesztése CertPattern-re | Az `IsCertPattern` csak szintaxist ellenőriz. A Relayben, a Formsban, a primitives-ben és az engine-ben nincs illesztő. | gap | Új kód, a Relay munkacsomagban. Nem Form-képesség (hitelesített tanúsítvány kell hozzá, amit a Forms nem tarthat). |
| G12 | A policy digestje a grant mellé | Az engine materializált objektumokat digestel (szemantikai). A release rekord nyers artifact-bájtokat digestel (`artifacts.files[].digest`), és a kanonizált rekordfát hash-eli (`build_hash`). | három digest-réteg | A grantbe kötött digest a **hitelesített policy-bájtok artifact digestje**, a 6. szakasz első rétege. Szemantikai digest kiegészítő mező lehet, helyettesítő soha. |
| G13 | Idő és lánc-érvényesség | A Forms trust terv 1–6. szakasza: a mostani lánc tanúsítványai 2026-12-31-én lejárnak (pontos időpontok a 7. szakaszban); a megújítás nem teszi érvényessé a régi aláírásokat; a lejárat előtti létezés bizonyítékát a lejárat előtt kell létrehozni; az ellenjegyzett payload a konstans `build_hash`, tehát nincs benne idő. | concept (terv), **határidő** | 7. szakasz. |
| G14 | A régi rekordok ellenőrizhetősége megmarad | A referencia-verifier az envelope 1 rekordot (`v0.1.5`) `envelope_too_old` okkal elutasítja, szerződés szerint; a `v0.2.1` rekord átmegy a `record` és `crypto` rétegen. A `blobs` réteg `NOT_RUN` volt (a forrás-commit nincs a sekély klónban). A `v0.2.0` rekord nincs a `v0.2.1` fában, nem futott. | implemented | A „változatlan ellenőrizhetőség” azt jelenti: az envelope 1 és 2 a compiler saját útján marad ellenőrizve, az envelope 3 bájtjai és a verifier ítéletei azonosak maradnak. Az általánosításhoz minden meglévő rekordra kell egy golden vektor. |

## 5. A próba és a parser-határ, mérve (G3, G4, G5)

### 5.1 A Forms illeszkedési próba

Egy reprezentatív `ModuleRelease` sémát (`docs/evidence/forms-fit-probe/schema.yaml`: beágyazott kompozitok, kötelező tagok, `path` kulcsú `artifacts.files` lista, `min_items`) lefordítottam a `compile_schema`-val, és tíz, a publikált `v0.2.1` rekordból származtatott dokumentumot (identitás és tanúsítványok helykitöltőre cserélve) validáltam és címeztem `path_of`-fal a `cic-primitive-forms` `2f02a75` Rust és Go könyvtárával. A Go és a Rust kimenete bájtra azonos (`expected-output.txt`).

| Eset | Eredmény, Rustban és Goban egyaránt |
|---|---|
| séma-fordítás | OK |
| 01 jól formált dokumentum | érvényes; a `path_of` a listaelemet identitásával nevezi meg: `$.artifacts.files[{path="dependency.yaml"}]` |
| 02 hiányzó `provenance.source_commit` | `MISSING_MANDATORY` a `$.provenance.source_commit` helyen |
| 03 ismeretlen tag a `provenance`-ben | `UNKNOWN_FIELD` a `$.provenance.grammar_sha256` helyen |
| 04 felső szintű `specs` | `UNKNOWN_FIELD` a `$.specs` helyen |
| 05 két `files` bejegyzés azonos `path`-szal | `PATH_DUPLICATE_IDENTITY` a `$.artifacts.files[{path="LICENSE.md"}]` helyen |
| 06 `envelope: '3'` (idézett string, ahol egész van deklarálva) | elutasítva: `UNSUPPORTED_SYNTAX` (hiba, nem violation-lista) |
| 07 `files: []` | `MIN_ITEMS_NOT_MET` a `$.artifacts.files` helyen |
| 08 `timestamp: 1.5` | parse-nál elutasítva: `UNSUPPORTED_SYNTAX` |
| 09 `timestamp: yes` | **érvényes**: a `yes` szövegként olvasva |
| 10 idézőjel nélküli `timestamp: 2026-11-02T18:57:58+00:00` | **érvényes**: `T`-s szövegként olvasva |

A 02–08 esetek mutatják, hogy a próba tud bukni; a 01 mutatja, hogy át tud menni. Amit bizonyít: a rekordfajta szerkezeti része kifejezhető a CIC séma nyelvén, az ismeretlen tag elutasítása, a listaidentitás duplikáció-észleléssel és a `min_items` működik, és a két implementáció egyezik. Amit nem bizonyít: az értékkorlátokat (nincs ilyen), a produkciós használatot, és hogy a valódi `ModuleRelease` séma lefordul (a próbaséma egy minimum). **Fenntartás:** a `compile_schema` csak a `cic-primitives-73ae469-6a188078` grammatika-profilt fogadja el, amely a cic-primitives egy fejlesztői commitja, és amelyet a Forms forrása maga átmeneti kompatibilitási profilnak nevez, nem releasenek. A Formsra épülő rekordséma ezért nem-release grammatika-pinhez kötött, amíg a Forms nem hordoz kiadott profilt.

### 5.2 A 09 és 10 eset: miért nem elég a zárt értéktartomány (review F1)

A rekord hash-e egy Python értékfa felett készül (`json.dumps(sort_keys=True, separators=(",",":"), default=str)` azon, amit a PyYAML adott). A Forms szándékosan szűkebb YAML-részhalmazt olvas: minden plain skalár, a `null`, `true`, `false` és a 64 bites decimális egészek kivételével, szöveg, a lebegőpontost, hexát, oktálist és a vezető nullás egészet pedig elutasítja. PyYAML minta, úgy kanonizálva, ahogy a compiler teszi:

| Bemeneti skalár | PyYAML, majd kanonikus JSON | Forms |
|---|---|---|
| `1.0` | szám `1.0` | elutasítva (`UNSUPPORTED_SYNTAX`) |
| `0x10` | szám `16` | elutasítva |
| `1_000` | szám `1000` | szöveg |
| `yes` | `true` | a `yes` szöveg |
| `2026-11-02T18:57:58+00:00` idézőjel nélkül | a `2026-11-02 18:57:58+00:00` szöveg (szóközzel, a `default=str` miatt) | a `T`-s szöveg |
| `1e3` | az `1e3` szöveg | elutasítva |

A felmérés korábbi vázlata zárt értéktartomány kimondását javasolta (string, integer, boolean, null, lista, mapping). **Ez nem elég**, ahogy a reviewer megmutatta: a `yes` és az `1_000` a tartományon belül van, mégis különbözően olvasódik (boolean és egész a két szöveggel szemben), és a próba megerősíti, hogy a Forms egyedül admitálja őket (a 09 és 10 eset érvényes). Az sem védelem, ha a tartományt kimondjuk, és a kibocsátó idézésére hagyatkozunk, mert a verifiernek akkor is tartania kell, ha a rekordot más írta.

Az admission feltétele ezért a Forms admission szerződés háromrészes feltétele, a második részt kifejezetten és a verifier által ellenőrzötten: **a rekordot csak akkor admitáljuk, ha megengedett, az általunk használt olvasók azonosan olvassák, és a provenance igazolt** (`P_allowed` és `P_parity` és `VerifiedProvenance`).

* **Értelmezési azonosság (`P_parity`).** A verifier rekord rétege a rekordot beolvassa azzal az olvasóval, amely a `build_hash`-t számítja, és egy második, független olvasóval (a Forms olvasója, Rust vagy Go), és új okkal (javasolt név: `scalar_reading_differs`) elutasít, ha a két típusos fa bármely skalárban eltér, azt is beleértve, ha az egyikben string, a másikban boolean, egész vagy dátum. A YAML-dialektustól függő olvasatú plain skalár így elutasított; az idézett stringet mindenki azonosan olvassa.
* **A kibocsátó** idézett stringeket ír, hogy a saját rekordjai átmenjenek; ez kényelem, soha nem a védelem.
* **Vektorok.** A 09 és 10 eset, a `yes`, `1_000`, `0x10`, `1.0`, `1e3` és egy idézőjel nélküli dátum conformance-vektor, amelyet a rekord rétegnek el kell utasítania, a Python referencia-verifierrel mint az eltérés orákulumával.
* Maga a hash-számítás nem változik.

### 5.3 A Relay a meglévő Forms Go implementációra épül (review F2)

A Relay rekord- és policy-olvasója a meglévő **Forms Go könyvtár** (`github.com/CentralInfraCore/cic-primitive-forms/go`), nem ugyanazon részhalmaz újraimplementálása. A Relay saját strukturális validátora, második parser vagy az admitált részhalmaz második másolata **nem fogadható el**: az egy második Form lenne. Eltérés csak **dokumentált, méréssel igazolt technikai akadály** esetén elfogadható (például feloldhatatlan függőségi vagy toolchain-ütközés, méréssel megmutatva), a döntésként a méréssel együtt rögzítve. Az importnak a Forms pontos verzióját kell rögzítenie, és az 5.1 szakasz sémakötési pinje érvényes.

## 6. A három digest-réteg, egymástól elválasztva (review F4)

A felmérés egy korábbi sora szerint „az aláírás és a digest mindig a szerzői bájtokon van”. Ez envelope 3-ra nem pontos. Három különböző digest van, és három különböző kérdésre felel:

| Réteg | Mi felett | Hol ellenőrzött | Mire felel |
|---|---|---|---|
| **Artifact digest** (`artifacts.files[].digest`, `manifest_digest`) | minden kiadott fájl **eredeti bájtjai**, ahogy a git blobban vannak, szűrő nélkül | a `blobs` réteg (és a Relay betöltött fájlja egyszeri olvasása) | ezek-e a kiadott bájtok |
| **Release hash** (`release.build_hash`) és az aláírások felette | **maga a rekord kanonikus értékfája** (a bundle a `release.sign`, `release.build_hash`, `cic_countersign` nélkül), a rekordfájl beolvasása után | a `crypto` réteg | ezt a rekordot, értelmezett faként, írta alá az aláíró és az ellenjegyző |
| **Szemantikai digest** (az engine) | az **értelmezett, materializált objektum** | az engine | mit jelent ez az objektum értelmezés után |

Következmények. Az aláírások **nem** a rekordfájl bájtjait fedik, hanem azt a fát, amelyet egy parser levezetett belőle. Éppen ezért az 5.2 értelmezési azonosság a rekord réteg biztonsági tulajdonsága, nem kényelem. A kiadott artifactok szerzői bájtjait külön köti az artifact digest. A normalizált nézet (Forms `normalize`) legfeljebb a harmadik réteghez tartozik, és az első kettőt soha nem helyettesíti: admission után megmutatható, de soha nem digest vagy aláírás bemenete. A grant mellé rögzített digest a hitelesített policy-bájtok első rétegbeli artifact digestje (G12).

## 7. Lánc-érvényesség és a 2026-12-31-i lejárat (review F5)

**Tények.** A Forms trust-terv 1. szakaszából (ott 2026-11-02-án mérve): a CIC Developer CA 2026-12-31 13:22:46 UTC-kor, a CIC Source CA 2026-12-31 13:22:39 UTC-kor, a fejlesztői tanúsítvány 2026-12-31 13:22:59 UTC-kor, a CIC Root CA 2026-12-31 23:59:59 UTC-kor jár le. **Itt újramérve a `v0.2.1` rekordból:** a fejlesztői tanúsítvány 2026-12-31 13:22:59 GMT-kor, a Source CA 13:22:39 GMT-kor jár le. A Developer CA és a Root időpontját a Forms tervből vettem, nem mértem újra. A rekordhoz a release-aláírás és az ellenjegyzés is kell, ezért az első pillanat, amikor a „most” időpontban végzett ellenőrzés elbukik, **2026-12-31 13:22:39 UTC** (a Source CA). A repó fejlesztői vállalása 2027-05-25-ig tart, de a fenti fejlesztői tanúsítványra támaszkodik. Ma 2026-11-17, 44 nappal előtte.

**Amit a megújítás nem tesz meg.** Az új tanúsítvány vagy lánc **nem** teszi érvényessé a régi aláírást. A régi aláírás a régi publikus kulccsal és a régi tanúsítvánnyal ellenőrződik, és rá csak a régi lánc érvényességi ablaka vonatkozik. Az új lánc új rekordot írhat alá, és új állítást egy régi rekordról (megújítási nyilatkozat); visszamenőleg nem hat. Ez a Forms trust terv 4. szakaszának álláspontja, amelyet ez a felmérés követ, és nem fogalmaz át.

**Mi következik a mostani lánc alatt aláírt rekordra** (a terv C1–C6 feltételei, egy verifier-policy feltételeiként): 2026-12-31 után csak akkor elfogadható, ha van független bizonyíték arra, hogy mindkét aláírás a lejárat előtt létezett (az aláíró saját `timestamp`-je és az aláíratlan `ts` nem számít), az érvényesség a bizonyított időpontban ítélt és nem „most”, a teljes lánc az akkori állapotban (a Developer CA-t a rekord nem hordozza), a visszavonásról kimondott álláspont, a régi root történelmi horgonyként az ablakával, és elfogadható algoritmusok. A bizonyítékot a lejárat előtt kell létrehozni; a később szerzett csak azt bizonyítja, hogy „nem később, mint” egy, a lánc megszűnése utáni dátum. Hogy ezt a `v0.2.1`-re és a régebbi releasekre megtesszük-e, a terv T3 tulajdonosi döntése, a határidejével.

**Mi következik a #162-re.** A Relay terve az érvényességet betöltéskor ellenőrzi először. As-of mód és bizonyítéka nélkül minden, a mostani lánc alatt aláírt modul 2026-12-31 13:22:39 UTC-től nem töltődik be. Az Oracle PoC-nak és minden tesztrekordnak ezért olyan lánccal kell aláírva lennie, amely túléli a tesztet, vagy teszt-tanúsítványokat használnak (a tesztterv a vektorokhoz már teszt-kulcsos rekordokat használ). Ez a PoC ütemezési korlátja; a terven nem változtat.

## 8. Mit javasol ez a felmérés

1. **Szerződés-általánosítás (cic-primitives, a következő megvalósítási PR, amint engedélyezett):** közös envelope rész (a szerződés 4. szakaszának tagjai, a hash-szabály, a verifier rétegei), fajtánkénti sémák (`PrimitiveRelease`, `ModuleRelease` és a policy-fajta), az 5.2 **értelmezési azonosság feltétele** a rekord rétegben a vektoraival (nem pusztán kimondott értéktartomány), és minden meglévő rekordra egy golden vektor, amely bizonyítja, hogy az ítélete nem mozdul. A hash-mechanizmuson nem változtatunk.
2. **Forms-változtatás nincs**: általános strukturális hiányt nem igazoltam, és az 5.1 próba mutatja, hogy a strukturális rész kifejezhető. A Formst ott használjuk, ahol már illeszkedik: az admission bejárat (G4), a listaidentitás (G6) és az olvasási normalizált nézet (G7). Az új Forms-képességek HOLD-on maradnak.
3. **A Relay a meglévő Forms Go könyvtárat használja** (5.3). Relay-saját strukturális validátor nem fogadható el.
4. **A D4 a Forms 7. szakasza trust policyjának kiterjesztése** (G9), nem új dokumentumcsalád: modul-hatókör és capability-plafonok hozzáadása; a horgonyok, aláírók, ellenjegyzők, mindkettő-kötelező és idő sorok változatlanok. A döntést a Relay hozza; a Forms csak azt oldja fel, ami adat.
5. **Registry:** séma a `capability_manifest`-hez (G10), mielőtt bármilyen plafon validálható.
6. **Relay munkacsomag** (még HOLD): a CertPattern-illesztő (G11), a rekordolvasó a Forms Go-n át, a grantbe kötött policy-digest a hitelesített policy-bájtokból (G12).
7. **Most felvetendő tulajdonosi döntések:** a Forms trust terv T3 és T6 pontja (időbizonyíték és lánc-utódlás, 2026-12-31 13:22:39 UTC előtt); és a rekordsémákhoz használt Forms grammatika-pin (5.1 fenntartás).

## 9. Nem igazolt

A próbaséma egy minimum, nem a valódi `ModuleRelease` séma; csak a fejlesztői grammatika-profil alatt futott. Forms- vagy engine-tesztcsomag nem futott, csak a próba. A `scalar_reading_differs` ok javaslat: nincs verifier, amely megvalósítaná, és vektor sincs még. Hogy a `PolicyResolution` alkalmazható-e policy-dokumentumra a meglévő atomokkal, nem próbáltam; objektummezőkre tervezettként olvastam. A `v0.2.0` rekordot és a `blobs` réteget a valódi forrás-commiton nem futtattam. A négy repón kívül nem kerestem `capability_manifest` sémát. A `capability_manifest` előfordulásait a Relayben és az Oracle modulban itt nem mértem újra. A Forms trust terv 2026-11-02-i; csak a 7. szakaszban nevezett két tanúsítvány-dátumot mértem újra. A Forms Go könyvtár importjának hatását a Relay függőségi és release-folyamatára nem mértem.
