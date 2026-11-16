# Modul-artifact hitelesítés (#162): architekturális javaslat

Státusz: **concept. Csak terv; kód és viselkedésváltozás nincs.** A megvalósítás a javaslat review-jáig HOLD. Társdokumentum: [artifact-authentication-test-plan.md](artifact-authentication-test-plan.md).

Alap: CIC_Relay `main@e58344a`; cic-primitives `devel@01cb30b` (friss, sekély klón, 2026-11-05); cic-module-oracle-cloud `devel@299733c`. Az alábbi státuszok a háromszintű szabályt követik (implemented / scaffold / concept), és a megnevezett fájl támasztja alá őket.

## 1. A kérdés

A #155 és a #197 után a relay eldönti, hogy egy modul milyen host műveleteket végezhet, és elutasítja az ellenőrizhető tanúsítvány nélküli modult. A következő kérdés: **bizonyíthatóan az a modul kapja-e ezeket a jogosultságokat, amelynek kiállították őket?** Ma a válasz az, hogy a modul egy megbízható CA-ig vezető láncú tanúsítványt hordoz, ez pedig sem a modult, sem a capability manifestet nem hitelesíti (#162).

## 2. Mi igaz ma

| Réteg | Státusz | Hol |
|---|---|---|
| A `.pem` tanúsítványlánca (EKU CodeSigning, CRL, frissesség) | implemented | `cmd/relay/bootstrap.go` `loadModule`; `core/cabinet/pki_verify.go` |
| A WASM digestje és mérete a leíróval szemben (`wasmDigest`, `wasmSize`) | implemented | `loadModule` |
| Az aláíró tanúsítvány a CICSourceCA horgonyig vezet, mielőtt `cic-flow`/`git` capability kötődik | implemented | `cmd/relay/pki_companion.go:69` `verifyCICFlowProvenance` |
| A grant a tárolt artifact checksumjához, a példány a manifest digestjéhez kötve az élettartamára | implemented | `cmd/relay/cic_flow_registry.go` `stageVerified`, `commitGrant`, `bindingFor` |
| **A tanúsítvány kulcsával készült aláírás a WASM és a manifest felett** | **concept** | a híd a `loadModule`-nál szakad meg: a tanúsítvány kulcsával készült aláírást semmi nem ellenőriz. A `metadata.cicSign` kiolvasott (`cic_flow_manifest.go:124`), de soha nem ellenőrzött; a `verifyBuildHash` (`cic_flow_manifest.go:312`) csak tesztből hívott |

A grant- és példánykötés valós, de azt köti, **ami be lett töltve**, nem azt, **ami hitelesítve lett**: semmi nem bizonyítja, hogy a relay által beolvasott manifestet bárki jóváhagyta. A `capability_manifest`-et hordozó leírót csak az mellette lévő érvényes tanúsítvány megléte hitelesíti.

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

**4.3 Jogosultság (a tulajdonos iránya).** Az aláíró kulcsnak bizonyíthatóan a **CICSourceCA** bizalmi lánchoz kell kapcsolódnia. A Vault Transit önmagában nem jogosultsági modell. Konkrétan: az ellenjegyző tanúsítvány a rögzített CICSourceCA gyökérig vezessen (a relay a `SourceRoots`-ot már betölti), EKU CodeSigning legyen, és ne legyen visszavonva; a `release.sign` a trust store-ig vezessen. Az érvényes lánc azt mondja: „a CICSource ezt a `build_hash`-t igazolja". Azt **még nem** mondja, hogy „ez az aláíró X nevű modult Y capabilityvel kiadhat" (lásd D4).

**4.4 Kötés.** A capability a `(modulazonosító, build_hash, wasm digest, manifest digest, aláíró ujjlenyomat)` ötöshöz kötődik, nem a modulazonosítóhoz. Ismert néven, más digesttel betöltött modul nem örökli a korábbi grantet; a futó példány megtartja a létrehozáskor kapott kötést (a meglévő `InstanceBinding` kiterjesztendő a `build_hash`-sel).

**4.5 Szabály.** `prod`-ban nincs automatikus visszaesés a csak tanúsítványalapú modellre: az ellenőrizhető rekord nélküli modul nem kap capabilityt és nem töltődik be (hatókör: D3). A meglévő modulokat újra ki kell adni; rekordot nem írunk át és nem „frissítünk" (primitives szerződés 9. szakasz). A `dev` megtartja a #197 explicit opt-injét a **rekord nélküli** modulra; az érvénytelen rekordot soha nem engedi be.

## 5. Hídtérkép

concept (ez a dokumentum) -> kód: a verifier, a kötési kulcs és a szabály nem létezik (**itt szakad meg a híd**) -> runtime: a betöltő csak a modulhármast olvassa -> audit: a betöltési napló a tanúsítvány subjectjét rögzíti, nem egy ellenőrzött `build_hash`-t. Meglévő, újrahasznosítható kapcsolatok: `verifyCICFlowProvenance` (horgony), `cicFlowRegistry` (grant- és példánykötés), a primitives vektorok és a referencia-verifier.

## 6. Tulajdonosi döntést igénylő pontok

| # | Döntés | Javaslat |
|---|---|---|
| D1 | A rekord helye: a modul mellett `<név>.release.yaml`, vagy a leíróba ágyazva | külön fájl: a rekord egy bundle felett aláírt, a beágyazás a leírót a saját aláírt bemenetévé tenné |
| D2 | A `.pem` sorsa: a rekord már hordozza a tanúsítványokat | `prod` a trusthoz nem a `.pem`-et használja, hanem a rekordot; a `.pem` csak dev/legacy |
| D3 | A „nincs visszaesés" hatóköre `prod`-ban: minden rekord nélküli modul elutasítva, vagy csak a `cic-flow`/`git`-et importálók | minden modul (egy szabály, nincs capabilityfüggő út, amit át kellene gondolni); megerősítendő |
| D4 | Mi jogosít fel egy aláírót egy **modulnévre és capability-készletre** (tanúsítvány subject/SAN minta, vagy aláírt engedélylista) | döntés kell; a lánc önmagában nem válaszol rá |
| D5 | Idő: a rekordnak nincs megbízható időbélyege; a tanúsítvány érvényességét betöltéskor, nem a release napján ellenőrizzük | első körben a betöltéskori érvényesség, a rés rögzítve; az időbélyeg külön tétel |
| D6 | A verifier megvalósítása | független Go implementáció a primitives vektorokkal, és differenciálisan a Python referenciával (a Forms Rust/Go gyakorlat) |
| D7 | Egyetlen release visszavonása (a CRL tanúsítványokat fed, nem `build_hash`-t) | itt hatókörön kívül; a tiltólista követő tétel |

## 7. Hatókörön kívül, és amit ez a felmérés nem ellenőrzött

Hatókörön kívül: a WASM bináris tényleges importlistájának betöltéskori ellenőrzése, a `git.push` újra bevezetése, az Oracle végponttól végpontig integráció. Nem ellenőrzött: a primitives compiler envelope 3 kimenete ma hogyan viselkedik (csak a szerződést, a verifiert és a vektorokat olvastam); a CICSourceCA ellenjegyzés pontos jelentése a `release.schema.yaml`-on túl; az Oracle release-futás ad-e valódi aláírt rekordot.
