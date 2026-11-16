# Modul-artifact hitelesítés (#162): architekturális javaslat

Státusz: **concept. Csak terv; kód és viselkedésváltozás nincs.** A megvalósítás a javaslat review-jáig HOLD. Társdokumentum: [artifact-authentication-test-plan.md](artifact-authentication-test-plan.md).

A #200 architekturális review-ja után átdolgozva (F1–F5 leletek: a `cic-flow` kötés gyengébb a gitnél, példány-identitás és jogosultság, az autorizáció külön döntés, ellenőrzött és végrehajtott bájtok, producer-kompatibilitás).

Alap: CIC_Relay `main@e58344a`; cic-primitives `devel@01cb30b` (friss, sekély klón, 2026-11-05); cic-module-oracle-cloud `devel@299733c`. Az alábbi státuszok a háromszintű szabályt követik (implemented / scaffold / concept), és a megnevezett fájl támasztja alá őket.

## 1. A kérdés

A #155 és a #197 után a relay eldönti, hogy egy modul milyen host műveleteket végezhet, és elutasítja az ellenőrizhető tanúsítvány nélküli modult. A következő kérdés: **bizonyíthatóan az a modul kapja-e ezeket a jogosultságokat, amelynek kiállították őket?** Ma a válasz az, hogy a modul egy megbízható CA-ig vezető láncú tanúsítványt hordoz, ez pedig sem a modult, sem a capability manifestet nem hitelesíti (#162).

## 2. Mi igaz ma

| Réteg | Státusz | Hol |
|---|---|---|
| A `.pem` tanúsítványlánca (EKU CodeSigning, CRL, frissesség) | implemented | `cmd/relay/bootstrap.go` `loadModule`; `core/cabinet/pki_verify.go` |
| A WASM digestje és mérete a leíróval szemben (`wasmDigest`, `wasmSize`) | implemented | `loadModule` |
| Az aláíró tanúsítvány a CICSourceCA horgonyig vezet, mielőtt `cic-flow`/`git` capability kötődik | implemented | `cmd/relay/pki_companion.go:69` `verifyCICFlowProvenance` |
| **git**: a grant a tárolt artifact checksumjához kötve, az artifact tárolásáig staged, a példány a manifest digestjéhez kötve | implemented | `cmd/relay/cic_flow_registry.go` `stageVerified`, `commitGrant`, `bindingFor`, `authorizeGit` |
| **cic-flow** (`sign`, `actuate`): a függőségek azonnal, a modulazonosító alatt publikálódnak; a lookup csak a modulazonosítót nézi; nincs artifact checksum, manifest digest, staging | implemented, **gyengébb a gitnél** | `cmd/relay/cic_flow_registry.go` `publish` (`r.deps[moduleID]`), `lookup` |
| **A tanúsítvány kulcsával készült aláírás a WASM és a manifest felett** | **concept** | a híd a `loadModule`-nál szakad meg: a tanúsítvány kulcsával készült aláírást semmi nem ellenőriz. A `metadata.cicSign` kiolvasott (`cic_flow_manifest.go:124`), de soha nem ellenőrzött; a `verifyBuildHash` (`cic_flow_manifest.go:312`) csak tesztből hívott |

A git grant- és példánykötés valós, de azt köti, **ami be lett töltve**, nem azt, **ami hitelesítve lett**: semmi nem bizonyítja, hogy a relay által beolvasott manifestet bárki jóváhagyta. A `cic-flow` capability még gyengébb: csak a modulazonosító a kulcsa, így a `publish` és a `lookup` olvasása alapján egy újratöltés előtt létrehozott példány az újratöltés utáni függőségeket oldja fel az azonosítóhoz. (A kódból olvasva; ehhez ez a felmérés tesztet nem futtatott.) Ma két biztonsági szint van, és a tervnek eggyel kell végződnie. A `capability_manifest`-et hordozó leírót csak az mellette lévő érvényes tanúsítvány megléte hitelesíti.

Producer oldal (cic-module-oracle-cloud): a `project.yaml` `metadata.buildHash`-ét a repó `verify_release`-e a `module.wasm` SHA-256-jához hasonlítja („integritás, nem reprodukció"); ugyanez az eszköz kimondja, hogy kriptográfiai aláírás-ellenőrzést nem végez. A munkafában a `sign` és a `cicSign` `TBD` helyőrző, amit a release-futás tölt ki.

## 3. Amit a cic-primitives már ad (envelope 3)

Olvasva: `docs/en/RELEASE-RECORD-CONTRACT.md`, `docs/en/RELEASE-RECORD-CONFORMANCE.md`, `tools/release_record_conformance.py`, `release.schema.yaml`.

| Elem | Újrahasznosítás |
|---|---|
| Rekordforma: `provenance.source_commit`, `artifacts.files[{path,digest}]`, `artifacts.manifest_digest` (sha256sum formátum), explicit útvonalszabályok | **változtatás nélkül** |
| `release.build_hash` a kanonikus bundle felett (minden, kivéve `release.sign`, `release.build_hash`, `cic_countersign`), `release.sign` (Vault ECDSA, prehashed, a tanúsítvány a hash-be kötve), `cic_countersign` a CICSourceCA-tól a `build_hash` felett | **változtatás nélkül** (új aláírási mechanizmus nincs) |
| Az okazonosítók szótára (`envelope_*`, `artifact_*`, `build_hash_mismatch`, `signature_invalid`, `countersign_invalid`) és az ellenőrzések sorrendje | **változtatás nélkül** |
| Golden és negatív vektorok, az aláírás-lefedettségi sorozat, a planted-defect bizonyítás | **változtatás nélkül**, nyelvfüggetlen tesztvektorként egy Go verifierhez |
| Python referencia-verifier három, külön jelentett réteggel (record, blobs, crypto) | **változtatás nélkül**, a differenciális tesztelés orákuluma |
| Producer-deklaráció `compiler_settings.release_artifacts.paths` | **változtatás nélkül**; a modul producere itt sorolja fel a modul fájljait |

**Producer-kompatibilitás, mérve.** Létezik valódi, compilerrel előállított envelope 3 rekord: a `primitives/@v0.2.1` release rekordja (tag commit `5e8c321`, `provenance.source_commit` `106965b`, 30 artifact, az ellenjegyzés megvan). A referencia-verifier (`tools/release_record_conformance.py`, a friss klónon futtatva) `record` PASS-t és `crypto` PASS-t jelent (`build_hash`, `signature`, `countersign` egyaránt PASS); a `blobs` NOT_RUN (nincs megadott repó), és maga a verifier mondja ki, hogy az aláíró kiléte nem bizonyított. A verifier-oldal tehát valódi compiler-kimeneten igazolt. **A modul-oldal nem:** a cic-module-oracle-cloud release-tooljának (a base-repo sablon compilere) nincs `release_artifacts` deklarációja, nincs `artifacts`/`manifest_digest` és `source_commit` (a `tools/`, `mk/`, `project.yaml` átkeresve); a `build-hash` lépése a WASM SHA-256-ját írja a `metadata.buildHash`-be. A `project.yaml` `envelopeVersion: 1` a WASM ABI boríték, nem release rekord. Egy modul ma tehát a saját toolingjából nem kaphat envelope 3 rekordot; ez a producer-munka a #162 része, és bármely valódi modulszintű teszt előfeltétele.

Amit a primitives **szándékosan nem ad** (szerződés 2. szakasz, „trust gate-ek"): az aláíró jogosultsága, az ellenjegyzés felhatalmazása, a tanúsítvány érvényessége a release napján, a rögzített trust anchor, az, hogy egy release valódi-e. A relaynek pontosan ezeket kell hozzátennie.

## 4. Javaslat

**4.1 Egy rekordformátum.** A modult envelope 3 release rekorddal adják ki. Relay-specifikus aláírási formátum nincs. A modul `release_artifacts.paths` listájának TARTALMAZNIA KELL a `.wasm`-ot és az `abi`-t és `capability_manifest`-et hordozó leírót/`project.yaml`-t; az a modul, amelynek rekordja nem fedi mindkettőt, elutasított.

**4.2 Négy réteg betöltéskor, külön jelentve.**

| Réteg | Kérdés | Forrás |
|---|---|---|
| record | jól formált-e a rekord (envelope 3, rendezett egyedi útvonalak, manifest digest)? | primitives okazonosítók, változatlanul |
| shipped | a relay által betöltendő bájtok egyeznek-e az `artifacts.files` digestjeivel? (a `blobs` helyett: a relaynek nincs git checkoutja) | új, relay |
| crypto | a `build_hash` újraszámolva; a `release.sign` és a `cic_countersign` ellenőrizhető felette | primitives algoritmus, Go implementáció a vektorokkal ellenőrizve |
| authority | jogosult-e ez az aláíró ezt a modult ezekkel a capabilitykkel kiadni? | új, relay (4.3) |

Egy le nem futott réteg soha nem számít átmenetnek.

**4.3 Jogosultság (a tulajdonos iránya).** Az aláíró kulcsnak bizonyíthatóan a **CICSourceCA** bizalmi lánchoz kell kapcsolódnia. A Vault Transit önmagában nem jogosultsági modell. Konkrétan: az ellenjegyző tanúsítvány a rögzített CICSourceCA gyökérig vezessen (a relay a `SourceRoots`-ot már betölti), EKU CodeSigning legyen, és ne legyen visszavonva; a `release.sign` a trust store-ig vezessen. Az érvényes lánc azt mondja: „a CICSource ezt a `build_hash`-t igazolja". Azt **nem** mondja, hogy „ez az aláíró X nevű modult Y capabilityvel kiadhat": **az aláírt artifact önmagában nem jogosultság; az autorizációs döntés egyik hitelesített bemenete.** A döntés külön lépés: ellenőrzött envelope -> ellenőrzött artifact és manifest -> az aláíró identitása és lánca -> egy *deklarált, hitelesített autorizációs szabály* -> a modulok és capabilityk halmaza, amelyeket ez az aláíró kiadhat -> artifacthoz kötött grant (bármely lépés bukásakor DENY). A szabály deklarált policy, amelyet a relay ellenőriz, nem tanúsítvány subject/SAN mezőbe kódolt minta (D4). A D4-en belül nyitott: ki írja alá a policyt, és hol él.

**4.4 Kötés, egy mechanizmus a gitnek és a cic-flow-nak.** A capability a `(modulazonosító, build_hash, wasm digest, manifest digest, aláíró ujjlenyomat)` ötöshöz kötődik, nem a modulazonosítóhoz, és **mind** a `git`, **mind** a `cic-flow` ugyanazt az artifacthoz kötött, staged-majd-commitolt grantet használja, amelyet a git ma is: a `cic-flow` erre kerül át (ma névkulcsos és azonnali). Nem lehet két biztonsági szint. Ismert néven, más digesttel betöltött modul nem örökli a korábbi grantet.

**4.5 Szabály.** `prod`-ban nincs automatikus visszaesés a csak tanúsítványalapú modellre: az ellenőrizhető rekord nélküli modul nem kap capabilityt és nem töltődik be (hatókör: D3). A meglévő modulokat újra ki kell adni; rekordot nem írunk át és nem „frissítünk" (primitives szerződés 9. szakasz). A `dev` megtartja a #197 explicit opt-injét, és az csak az **artifact-hitelesítés nélküli** betöltést engedi: soha nem kerüli meg egy hibás rekord ellenőrzését, és soha nem eredményez hitelesített capability grantet.

**4.6 A példány identitása változtathatatlan, a jogosultsága visszavonható.** Egy példány megtartja a létrehozáskor kapott identitását (modulazonosító, artifact, kötés). Hogy mit *tehet*, azt minden híváskor döntjük el, és visszavonható:

| Esemény | A korábbi példány jogosultsága |
|---|---|
| új manifest betöltése | DENY |
| másik artifact betöltése | DENY |
| sikertelen újratöltés | DENY |
| a grant visszavonása | DENY |
| változatlan kötés és aktív grant | ALLOW, a szabályokon belül |

Az A -> B -> A életciklus külön tesztelt: a korábbi identitás visszaállítása önmagában nem engedélyezi újra azt a példányt, amely közben DENY volt. (Ez váltja a korábbi „manifestváltáskor is megtartja a jogosultságát" megfogalmazást, amely ellentmondott a #155 fail-closed működésének.)

**4.7 Az ellenőrzött bájtok a végrehajtott bájtok.** (a) A példány a verifier által ellenőrzött WASM-bájtokból készül; (b) a grant a verifier által ellenőrzött leíróból készül; (c) ellenőrzés után nincs fájlrendszerről történő újraolvasás; (d) a rekord útvonalainak megfeleltetése a betöltött fájlokhoz egzakt, fájlnév-alapú fallback nélkül; (e) a grant csak a teljes ellenőrzés és az artifact sikeres tárolása után válik aktívvá. Ma a WASM-ot a `loadModule` egyszer olvassa, és ugyanazok a bájtok adják a digest-ellenőrzést és a leírót; a leírót és a `.pem`-et a hívóik olvassák. A terv fájlonként egy olvasást tart, és azokat a bájtokat használja újra.

## 5. Hídtérkép

concept (ez a dokumentum) -> kód: a verifier, a kötési kulcs, a policy és a `cic-flow` artifact-kötés nem létezik, és a modul release-tooling nem tud envelope 3 rekordot kiadni (**itt szakad meg a híd, a relay és a producer oldalán is**) -> runtime: a betöltő csak a modulhármast olvassa -> audit: a betöltési napló a tanúsítvány subjectjét rögzíti, nem egy ellenőrzött `build_hash`-t. Meglévő, újrahasznosítható kapcsolatok: `verifyCICFlowProvenance` (horgony), `cicFlowRegistry` (grant- és példánykötés), a primitives vektorok és a referencia-verifier.

## 6. Tulajdonosi döntést igénylő pontok

Az alábbi javaslatok a #200 architekturális review-jából származnak; mindegyik a tulajdonos megerősítésére vár.

| # | Döntés | Javaslat |
|---|---|---|
| D1 | A rekord helye | külön `<név>.release.yaml` |
| D2 | A `.pem` sorsa | a release rekord az autoritatív forrás; a rekorddal ellentmondó legacy `.pem` DENY |
| D3 | A „nincs visszaesés" hatóköre `prod`-ban | minden modul, kivétel nélkül |
| D4 | Mi jogosít fel egy aláírót modulnévre és capability-készletre | deklarált, hitelesített autorizációs policy (nem tanúsítvány subject/SAN minta). Nyitott benne: ki írja alá a policyt, és hol él |
| D5 | Idő | a betöltéskori érvényesség első lépésnek elfogadható; a rés rögzítve |
| D6 | Verifier | Go implementáció, közös tesztvektorok, a Python referencia-verifier mint orákulum |
| D7 | Egyedi release visszavonás | külön issue; a hiányzó garanciát dokumentálni kell |

A `dev` opt-in csak az artifact-hitelesítés nélküli betöltést engedi. Soha nem kerüli meg a hibás rekord ellenőrzését, és nem eredményez hitelesített capability grantet.

## 7. Hatókörön kívül, és amit ez a felmérés nem ellenőrzött

Hatókörön kívül: a WASM bináris tényleges importlistájának betöltéskori ellenőrzése, a `git.push` újra bevezetése, az Oracle végponttól végpontig integráció. Ebben a revízióban ellenőrizve: a valódi `primitives/@v0.2.1` envelope 3 rekord a referencia-verifierrel `record` és `crypto` PASS. Nem ellenőrzött: modulszintű rekord (a modul-tooling nem tud ilyet kiadni, 3. szakasz); a CICSourceCA ellenjegyzés pontos jelentése a `release.schema.yaml`-on túl; az Oracle release-futás ad-e valódi aláírt rekordot.
