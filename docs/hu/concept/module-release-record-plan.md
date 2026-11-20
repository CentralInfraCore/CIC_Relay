# Modul-release rekord kibocsátó és a D4 policy-felmérés (#162, 1. munkacsomag)

Státusz: **concept.** Semmi nincs megvalósítva. Ez a dokumentum a review előtti terv, producer-kód még nem készül. Folytatása az [artifact-authentication.md](artifact-authentication.md) 3. és 6. szakaszának és a [tesztterv](artifact-authentication-test-plan.md) A19 tesztjének. A Relay implementáció, az Oracle végponttól végpontig teszt és a produkciós jóváhagyás HOLD.

## 1. Miről kell dönteni a reviewban

1. A modul-release rekord kibocsátó konkrét terve (4. szakasz).
2. A D4 javaslat: hogyan fejezhető ki az authorization policy (6. szakasz).
3. A 7. szakasz nyitott pontjai.

A tulajdonos döntött, és nem nyitjuk újra: a célág a base-repo `wasm/main` (D1), a legacy `cicSign` változatlan marad (D2), a munka szelektív átvétel önálló kibocsátóval, nem a primitives compiler átvitele (D3). Az egyetlen normatív release-szerződés a cic-primitives envelope 3 (`docs/en/RELEASE-RECORD-CONTRACT.md`, `primitives/@v0.2.1`). Nincs WASM-specifikus envelope és nincs második kanonizálás.

## 2. Mit talált a felmérés (base-repo, csak olvasás, friss klónok)

A státuszszavak a háromszintű skálát követik.

| Hol | Lelet | Státusz |
|---|---|---|
| `wasm/main` (`ae79934`) | A release-fázisok a `tools/infra.py` `ReleaseManager`-ben vannak: checksum és Vault-aláírás a `{name, version, checksum, build_timestamp}` felett. A `tools/finalize_release.py` ezen az úton elavult, halott kódnak jelölt (`wasm/f/finalize-deprecation`, `4b27eca`). **Ez javítja a korábbi állításomat:** a `finalize_release.py` teljes-`project.yaml` `cicSign`-ja nem az aktív mechanizmus a `wasm/main`-en. | implemented |
| `wasm/main` `tools/schemalib/artifact.py` | A `generate_signed_artifact` a `buildHash`, `cicSign` és `cicSignedCA.certificate` mezőt üres helykitöltőként írja („relay tölti ki”). Semmi nem tölti ki. | scaffold |
| `wasm/main` `tools/verify_release.py` | Offline készenléti ellenőrzés: séma, újraépítés a `buildHash` ellen, ABI exportok, `MANIFEST.sha256`. A provenance mezőket csak `OK/TBD/MISSING`-ként jelzi, aláírást nem ellenőriz. | implemented, kifejezetten nem kriptográfiai |
| `wasm/main` `docs/contracts/en/release-artifact.md` | Kimondja a célállapotot („bizonyítható, aláírt release csomag”: wasm + `project.yaml` + aláírás, offline ellenőrizhető), és a formátumát hatókörön kívülinek mondja, release-pipeline auditra várva. **Ezt a tételt szállítja ez a munkacsomag.** | concept |
| `wasm/f/release-contracts` (`c0ec14a`) | A szerződés-dokumentumok és a `verify_release.py`; az ág nem őse a `wasm/main`-nek, de a fájl a `wasm/main`-en megvan. Envelope, `source_commit`, artifact digest vagy ellenjegyzés nincs benne. | nincs átfedés |
| `feature/release-phases-port` (core `devel` vonal) | `run_check`, `run_prepare_release`, `run_finalize_release`. A valódi lezárás **tiltott**, mert a finalizálás visszamergeli a release ágat, amit a release-modell tilt. Envelope 3 fogalom nincs benne. | scaffold, folyamatban, nincs nyitott PR |
| `feature/signing-from-factory-core` (core `devel` vonal) | A commit- és tag-aláíró eszköztár. A tag manifest más digest, mint az `artifacts.manifest_digest`. | ott implemented, külön ügy |
| base-repo nyitott PR-ok | #41 (manifest újragenerálás), #42 (base verzió 0.8.0). Egyik sem érinti a release rekordot. | nincs átfedés |

Következtetés: egyik ág sem épít envelope 3 rekordot, tehát a kibocsátó nem párhuzamos megoldás egy folyamatban lévő mellett. Két kapcsolatot tiszteletben kell tartani: a kibocsátó nem függhet a `ReleaseManager` lezárási útjától (tiltott, amíg a core finalizálás nem felel meg), és nem használhatja a `schemalib.generate_signed_artifact`-et, amelynek helykitöltői a legacy alakhoz tartoznak.

## 3. Újrahasznosított mechanizmusok és az egyenlőség a primitives-szel

A kibocsátó a primitives mechanizmusokat saját modulban valósítja meg, mert a primitives `compiler.py` egy 1300 soros monolit, amelynek architektúráját nem visszük át. Az egyenlőséget nem feltételezzük: **a primitives kóddal és a primitives referencia-verifierrel teszteljük.**

| Mechanizmus (primitives `tools/compiler.py`, `primitives/@v0.2.1`) | A kibocsátóban |
|---|---|
| `_release_hash_payload`, envelope 3 szabály | A teljes bundle, kivéve `release.sign`, `release.build_hash`, `cic_countersign`; kanonikus JSON `sort_keys`, elválasztók `(",",":")`, `default=str`. |
| `_release_artifact_path_problem` | Ugyanazok az útvonalszabályok (szerződés 7. szakasz), ugyanazokkal az ok-azonosítókkal. |
| `_validate_release_artifact_declaration`, `_build_release_artifacts` | A deklaráció a **forrás-commit** `project.yaml`-jából, csak a commit fájából feloldva, blob-bájtok szűrő nélkül, `sha256:` digestek, UTF-8 bájtsorrend, `sha256sum` formátumú manifest digest. |
| `_vault_sign` (prehashed ECDSA), tanúsítvány a hash-ben | A meglévő base-repo `tools/releaselib/vault_service.py`-n át. |
| CICSourceCA ellenjegyzés a `build_hash` felett | Beinjektált ellenjegyzőn át; produkcióban a meglévő CICSourceCA agent folyamat (`vault-rootCA-sign-agent.sh` és `cic-countersign`). |

Egy pin fájl rögzíti a primitives commitot és minden átmásolt referencia-fájl SHA-256-ját, ahogy a Relay tesztterv a vektoroknál előírja.

## 4. A kibocsátó

Hely: új `tools/release_record/` modul a `wasm/main`-ből induló `wasm/f/release-record-envelope3` feature ágon. Az `infra.py`-t, a `finalize_release.py`-t és a legacy `cicSign`-t nem szerkesztjük.

**Sorrend (a rekord az utolsó lépés, befagyasztott bájtok felett):**

1. A `make wasm.build` beállítja a `metadata.buildHash`-t a `project.yaml`-ban. A WASM és a végleges `project.yaml` commitolva. Ez a forrás-commit.
2. Az `emit` a deklarációt, a blobokat és a fát **csak ebből a commitból** olvassa. Elutasít, ha bármely deklarált útvonal szállított munkafa-bájtja eltér a commit blobjától (`shipped_differs_from_commit`): a rekordnak a ténylegesen szállított bájtokat kell kötnie.
3. Összeállítja a bundle-t, kiszámítja a `build_hash`-t, beszerzi a `release.sign`-t, majd a `cic_countersign`-t, és kiírja a `<név>.release.yaml`-t (D1). A rekord maga nem artifact, tehát nincs körkörösség. Utólag commitoljuk, majd jön a tag (release-modell: devel → main → tag, visszamerge nincs).
4. **Publikálás-ellenőrzés (review F2).** A kibocsátás előtti ellenőrzés a rekordot a forrás-commithoz köti; azt nem bizonyítja, mi kerül publikálásra. A tag után és minden publikált másolatra a `verify-published` ellenőrzi: a tag olyan commitra mutat, amelynek fája a forrás-committól csak a rekordfájlban tér el (más hozzáadott vagy módosított útvonal nincs); minden deklarált artifact blobja a tagnél egyezik az aláírt rekord digestjével; és az artifact minden terjesztett másolata (release asset, OCI vagy archívum tartalom, a Relay által később betöltött másolat) bájtra egyezik a rekord digestjével. Bármely eltérés elutasítás saját okkal (`tag_tree_differs`, `published_artifact_mismatch`, `published_artifact_absent`). A rekord lehet helyes úgy is, hogy más tartalom kerül publikálásra; ezt zárja ki ez a lépés, és a producer release-jobjában fut, mielőtt a release meghirdetésre kerül.

**Deklaráció.** A `project.yaml` `compiler_settings.release_artifacts.paths`, pontosan a szerződés 6. szakaszának kulcsa és szabályai (explicit útvonalak, glob nélkül, ugyanazok az elutasítási azonosítók). A `project.schema.yaml` a `compiler_settings`-en `additionalProperties: false`-t deklarál, ezért a séma megkapja ezt a kulcsot. Az emitter egy **producer-oldali** szabályt tesz hozzá **a deklarációra**: a `module/module.wasm` és a `project.yaml` is deklarált kell legyen (`declaration_required_path_missing`; a cic-primitives #38 (merge commit `e364548`) P01 követelménye). Így köti a rekord együtt a WASM-ot és a teljes leírót (`abi`, `capability_manifest`). Ugyanez a tény a kész rekordon **verifier-oldali** kötelezettség is (V09, 10. szakasz): a producer-szabály nem váltja ki, és egyik sem helyettesíti a másikat. A deklarációs szabály önmagában nem változtat a szerződésen; a verifier-oldali szabály a release sémakészlet ModuleRelease részének követelménye (10. szakasz).

**Rekord-tagok.** `kind: ModuleRelease`, `name`, `version`, `timestamp`, `validity`, `createdBy`, `provenance{envelope:3, source_commit}`, `artifacts`, `release{envelope:3, createdBy, build_hash, sign}`, `cic_countersign`. A `specs` elmarad. **A `name` kötelező, ModuleRelease-specifikus tag** (a cic-primitives #38 (merge commit `e364548`) P5.1 döntése): nincs a közös envelope-ban, a PrimitiveRelease rekord nem hordozza; a release aláírt identitásának része, és a **hitelesített moduldescriptor identitásához kötött** (V08, 10. szakasz), nem puszta artifact-hivatkozás. A descriptorhoz és a WASM-hoz nem kerül további mutató tag az envelope-ba: a kötést az `artifacts.files` adja.

**Szerződéshatár (review F3).** A primitives envelope 3 szerződés és a `release.schema.yaml` a `PrimitiveRelease`-re szól: a séma `kind: PrimitiveRelease` értéket (`const`) és nem üres `specs`-et követel. Egy `ModuleRelease` rekord ezért **elbukik a jelenlegi sémán**, és a referencia-verifier átmenete nem bizonyít sémamegfelelést (a rekord rétege az `envelope`, `source_commit` és `artifacts` mezőt olvassa, a `specs`-et csak duplikáció-ellenőrzésre). A kibocsátót tehát egy szerződéses lépés blokkolja, amelyet a terv előfeltételként rögzít, nem feltevésként:

1. Külön cic-primitives változtatás (tulajdonosi merge, nem ebben a repóban), amely szétválasztja a **közös envelope-ot** (`provenance.envelope`, `provenance.source_commit`, `artifacts`, `release`, `cic_countersign`, a hash-szabály) a **típusspecifikus tagoktól** (`PrimitiveRelease`: `specs` és a primitive provenance-digestek; `ModuleRelease`: `name` és az artifact-készlet szabályai), `kind` szerinti sémával, amely osztozik a közös részen, és a verifier `kind` szerinti viselkedésével. **Állapot:** e szétválasztás terve vázlatként mergelve a cic-primitives #38-ban (merge commit `e364548`) (`docs/design/release-schemas/`: `common.yaml`, `primitive-release.yaml`, `module-release.yaml`, az összeállítási szabályok és a megfelelőségi deklaráció). A vázlatok **nem véglegesek** (a véglegesítés a grammatika node-név szabályára vár, cic-primitives #39), származtatott JSON Schema nincs.
2. Amíg a `kind` szerinti sémák nem véglegesek, fogyasztónak szánt `ModuleRelease` rekord nem készül. A producer-munka ideiglenesnek jelölt vektorokkal indulhat.
3. A rekordot a verifier mellett a `kind` szerinti séma ellen is validáljuk (5. szakasz, V1).

**Legacy elválasztás (review F1).** Két állítás, egymástól elválasztva:

* **Bizalom.** A `project.yaml` `cicSign`, `sign` vagy `checksum` mezőjének tartalma soha nem bizalmi bemenet. A kibocsátó döntéshez nem olvassa, fogyasztó nem fogadhatja el a rekord helyett, és a rekord nem fallback hozzá.
* **Kötés.** A `project.yaml`-t nyers bájtként kötjük, ezért ezek a mezők a kötött bájtok részei, mint bármi más. **Az aláírás után megváltoztatásuk megváltoztatja a leíró digestjét, és a rekord ekkor elbukik** (`artifact_digest_mismatch` a blobs rétegben; a Relay elutasít). A terv korábbi vázlata az ellenkezőjét állította; ez hibás volt.

A sorrend ebből következik: minden legacy mezőnek végleges alakban (kitöltve vagy `TBD`) kell lennie a forrás-commit **előtt**. Az a legacy finalizálás, amely a rekord aláírása után átírja a `project.yaml`-t, érvényteleníti a rekordot, ezért nem futhat a rekord után, és a kibocsátó elutasít, ha a munkafa nem a commitolt bájt.

**Nyelvek közötti kockázat.** A Python `json.dumps` YAML-típusú értékek felett az a kanonizálás, amit a szerződés örököl. A (későbbi) Go verifiert közös vektorok kötik ugyanazokhoz a bájtokhoz; a kibocsátó ezen felül rögzített, trükkös értékű korpuszt (nem ASCII, beágyazott, időbélyeg, lebegőpontos) tesztel a primitives függvénnyel szemben.

## 5. Bizonyítási terv (producer-oldali, önálló)

Elfogadás: a kibocsátó rekordja átmegy a primitives referencia-verifieren, a `tools/release_record_conformance.py`-n mind a három rétegen, **a blobokat is beleértve**, ideiglenes git repóban, valódi commit mellett. A Relay nem szükséges.

* **V1 (tagkészlet).** A `specs` nélküli, `kind: ModuleRelease`, `name`-et hordozó modul-rekord átmegy a `record`, `blobs` és `crypto` rétegen. A `name` nélküli rekord, és a `name`-et hordozó PrimitiveRelease rekord elutasított (szerkezeti, a módosítatlan Formon megfigyelve a cic-primitives #38-ban (merge commit `e364548`)).
* **Pozitív.** A teszt-kulcsok rögzített címkékből levezetve (mint a primitives vektoroknál); teszt-tanúsítványlánc helyettesíti a Vaultot és a CICSourceCA-t; valódi `git` commitok.
* **Differenciális.** A kibocsátó `build_hash`-e és `artifacts`-a a primitives `_release_hash_payload` és `_build_release_artifacts` ellen generált bemeneteken; bármilyen eltérés bukik.
* **Negatív (a kibocsátó elutasít).** Minden deklarációs ok, `declaration_required_path_missing`, `shipped_differs_from_commit`, symlink vagy submodule útvonal, a commitban hiányzó útvonal, hiányzó forrás-commit.
* **Negatív (a verifier elutasítja a kibocsátás utáni mutált rekordot).** A szerződés 11. szakaszának minden okához egy mutáció, mind azzal az okkal bukik: envelope, `source_commit`, artifact digest, manifest digest, sorrend, egy módosított WASM-bájt (blobs réteg), egy módosított `project.yaml` bájt vagy egy `capability_manifest` bejegyzés, cserélt tanúsítvány, `build_hash`, `release.sign`, `cic_countersign`.
* **Leíró és WASM kötés.** Bármelyik kivétele a deklarációból: a kibocsátó elutasítja; a kész rekordból kivéve: a `build_hash` bukik.
* **Elválasztás (F1).** Érvényes `cicSign`-os, rekord nélküli fa nem kap rekord-alapú átmenetet. Az a rekord, amelynek `project.yaml`-jában a `cicSign` (vagy `sign`, `checksum`) az aláírás után változik, **elbukik** a blobs rétegen artifact-digest okkal; a rekordot nem menti meg egy érvényes `cicSign`, és az érvényes rekordot nem érinti, ha a legacy érték a kibocsátáskor hibás (csak kötött, soha nem bizalmi).
* **Publikálás (F2).** Az a tag, amelynek fája a rekordfájlon kívül mással tér el a forrás-committól, a tagnél módosult deklarált artifact, egy bájtban eltérő terjesztett másolat és egy hiányzó másolat egyaránt elbukik a `verify-published`-en a megnevezett okkal; a változatlan publikáció átmegy.
* **Séma (F3).** A kibocsátott rekord átmegy a `kind` szerinti sémán, amint az végleges; addig a cic-primitives #38 (merge commit `e364548`) vázlatai a referencia, a vektorok ideiglenesek maradnak. A `kind: ModuleRelease` rekord a jelenlegi `PrimitiveRelease` sémán várhatóan elbukik (rögzített negatív).
* **Mutációs bizonyíték** a kibocsátóra: útvonal-rendezés, digest-előtag, hash-kizárási készlet, blob kontra munkafa, újsor-szűrés, aláírási lépések sorrendje, mind beültetett hiba, amelyet egy tesztnek meg kell ölnie.

## 6. D4 felmérés: az authorization policy

**Ami létezik.**

* A cic-primitives a `CertPattern`-t típusként definiálja, és az `Access` (`access`, `modify`) és a `PolicySurface` (`default_access`, `default_modify`, `surface_rules`) használja, `access-policy/1` grammatika. Ezek azt mondják meg, **ki olvashat vagy írhat egy kezelt objektum mezőit**, futásidőben mTLS tanúsítvány ellen kiértékelve. A mintaszintaxis idézett, mintán belül `ÉS` (`"OU=adapters,O=cic"`), a listaelemek között `VAGY`.
* A Forms megvalósítja az `IsCertPattern`-t (szintaxis) és a policy **feloldást**, amely effektív szabályt ad adatként (`list<CertPattern> | open | owner | none | unknown`). A tervdokumentuma maga mondja ki a határt: nincs allow vagy deny, nincs CertPattern-illesztés, nincs owner-bizalom vagy identitás, nincs hitelesítés.
* A Relay-ben nincs CertPattern-illesztő (a Go források keresése egyet sem talál).
* A `cic-countersign` a hívó által adott `workflow` szöveget fogadja, és nem allowlistel; a placeholder alapérték maga mondja, hogy az ellenjegyzés még nincs workflow-ra szűkítve.

**Ami hiányzik.** A primitives és a Forms semmiben nem mondja meg, *melyik aláíró melyik modult adhatja ki milyen capability-plafonnal*, és semmi nem illeszt tanúsítványt mintára. A meglévő szókincs objektummezőkről szól, nem kiadási jogokról.

**Javaslat.**

1. A **CertPattern grammatikát** használjuk az aláíró kiválasztására, hogy ne legyen második mintanyelv. A `PolicySurface` öröklését **nem** használjuk: a kiadási szabály lapos és alapból tiltó.
2. Új dokumentumfajta, `ReleaseAuthorizationPolicy`: szabályok listája `{module, signers: list<CertPattern>, ceilings: {egress, secrets, git, ...}}`. A hiányzó szabály DENY. A hitelesített modul `capability_manifest`-je az illeszkedő szabály plafonján belül kell legyen (A10).
3. A policy maga is ugyanazon envelope 3 **release rekord**, ugyanazzal a verifierrel ellenőrizve, egy policy fájl felett. Egy hitelesítési mechanizmus, második aláírásformátum nélkül.
4. Külön **policy-kiadási jog**, a Relay trust konfigurációjában rögzített CertPattern-ként, amely modul-aláírásból nem szerezhető meg (önjogosítás nincs, A21, A22). A trust gyökér a CICSourceCA lánc; a kiadói minta az új döntés.
5. A CertPattern illesztő a hitelesített aláíró tanúsítványra **új Relay-kód**, és még nem létezik. A Relay munkacsomaghoz tartozik, a szintaxishoz a Forms `IsCertPattern` vektorait újrahasznosítva.
6. **Az ellenjegyző jogosultsági határa (review F4).** A countersign hatókör-rés (fent) trust gate, amelyet le kell zárni, mielőtt a policy értelmes lehet, és a `workflow` szöveg allowlistelése nem elég, mert azt a hívó adja. A szolgáltatásnak kérésenként maga kell ellenőriznie: **ki** kéri (hitelesített kérő-identitás, nem szabad mező); a kérő **melyik rekordfajta** ellenjegyzésére jogosult (a szolgáltatás által tartott kérő–fajta tábla); hogy a **tartalom valóban ez a fajta** (a szolgáltatás parse-olja a rekordot, ellenőrzi a `kind`-ot, a sémát és az envelope-ot, nem ellenjegyez átlátszatlan hash-t); és hogy **a modul kiadója nem bővítheti a saját jogát**, tehát a `ModuleRelease` ellenjegyeztetésének joga nem a `ReleaseAuthorizationPolicy` ellenjegyeztetésének joga. A policy kiadása és a modul kiadása két bizalmi határ, két fajtával és két kérő-halmazzal. Ez a `cic-countersign` változtatása (külön repó és munkatétel); a `cic-module-release@v1` maradhat címke, hatalom soha.
7. **Policy-szemantika (review F5, külön munkacsomag, nem blokkolja a kibocsátót).** A D4 policy-specifikációban rögzítendő: a capability-plafon összehasonlításának pontos szabálya (capability-osztályonként halmaz-tartalmazás vagy numerikus korlát, és mit jelent a „szélesebb”), az átfedő szabályok kezelése (javaslat: a legspecifikusabb modul-illesztés nyer, azonos specifikusság hiba és DENY), és a már kiadott grantek sorsa policy-változáskor (javaslat: a grant rögzíti a policy digestjét, a megváltozott policy a következő betöltéskor érvényteleníti, a futó példányokra a terv visszavonási szabályai vonatkoznak).

## 7. Nyitott pontok a reviewernek

* ~~Elfogadható-e a `kind: ModuleRelease` `specs` nélkül~~ **Eldöntve** (cic-primitives #38 (merge commit `e364548`)): a ModuleRelease rész nem hordoz `specs`-et; saját kötelező `name`-je van; a közös/kind szétválasztás a release sémakészlet vázlataiban.
* ~~A producer-oldali szabály, hogy a `module/module.wasm` és a `project.yaml` deklarált kell legyen~~ **Eldöntve** (P5.2): producer-oldali (P01) **és** verifier-oldali (V09), mindegyik pontosan egyszer, további artifactok megengedettek (10. szakasz).
* A workflow azonosító és az allowlist-változtatás a `cic-countersign`-ban.
* A policy-kiadói minta: hol rögzített és hogyan rotálható.
* Lehet-e a modul aláírója és az ellenjegyző ugyanazon személy kulcsútvonala (feladatelválasztás).
* A cic-primitives `kind` szerinti szerződésváltozása (F3): a terv vázlatként mergelve (cic-primitives #38 (merge commit `e364548`)); nyitott: a véglegesítés (a cic-primitives #39-re vár), a származtatott JSON Schema, és hogy indulhatnak-e az ideiglenes vektorok (indulhatnak, ideiglenesnek jelölve).
* A `verify-published` lefedi-e a Relay által később letöltött másolatokat is (F2), vagy csak a producer release-jobját.

## 8. Amit ez a felmérés nem igazolt

A primitives `release_record_conformance.py`-t csak a publikált `v0.2.1` rekordon futtattam, modul-rekordon nem. Azt, hogy a countersign szolgáltatás elfogad-e modul-release kérést, és milyen tanúsítvány-subjectet adna, nem teszteltem. Forms futásidőt nem futtattam. A `wasm/f/hardening`, `wasm/f/manifest-fix` és `wasm/f/ci-followup` ágakat a csúcsukon túl nem olvastam. Az Oracle modul saját fáját ebben a lépésben nem mértem újra.

## 9. Sorrend

A terv review-ja; a cic-primitives `kind` szerinti szerződésváltozása (F3); utána először a producer-vektorok (mindegyik elbukik, mielőtt a kibocsátó létezik), majd a kibocsátó, majd egy base-repo PR a `wasm/main`-re. A Relay implementáció csak azután indul, hogy a rekord-szerződés és a D4 policy-szerződés végleges.

## 10. Szinkron a release sémakészlettel (cic-primitives #38)

Forrás: cic-primitives #38 (merge commit `e364548`), `docs/design/release-schemas/contract-conformance.yaml` (V08, V09, P01 követelmények) és `docs/en/RELEASE-SCHEMA-SET-DESIGN.md` 7. szakasz (P5). Csak dokumentáció: semmi nem változtat kódot, sémát, szerződést vagy verifiert. A producer- és verifier-oldali kötelezettségek elkülönítve:

| Azonosító | Végrehajtó | Állapot ott | Kötelezettség ModuleRelease rekordra |
|---|---|---|---|
| P01 | producer (az emitter) | planned | elutasítja azt a deklarációt, amelyből hiányzik a `module/module.wasm` vagy a `project.yaml` (`declaration_required_path_missing`), még rekord kibocsátása előtt |
| V08 | verifier | planned | a top-level `name` egyezzen a **hitelesített moduldescriptor** (`project.yaml`, az `artifacts.files`-on át kötve) identitásával; a `module_identity_mismatch` reason-azonosító a vázlat javaslata, a szerződésnek még nem része |
| V09 | verifier | planned | a `module/module.wasm` és a `project.yaml` **pontosan egyszer** szerepeljen a hitelesített artifactlistában; a hiányzó vagy ismétlődő elutasított (`kind_schema_violation`) **kriptográfiailag hibátlan rekord esetén is**: az aláírás a tartalom hitelességét bizonyítja, nem a modul-release szemantikai teljességét; további artifactok megengedettek |

Amit a módosítatlan Form a vázlatokkal tett (ugyanaz a PR, `docs/evidence/release-schema-draft-probe/`, Rust és Go bájtazonos): a hiányzó `name` elutasított, a `name` PrimitiveRelease rekordban elutasított; a descriptor identitásától eltérő név, a rossz `kind` és a hiányzó modul-artifact **átmegy** (a Form nem értékel ki contractot), ezért a V08 és a V09 a verifiernél marad, a felületi `must` bejegyzéseket a Form nem is jelenti (cic-primitive-forms #167).

Itt nem ellenőriztem: hogy a V08, V09, P01 bármelyike implementált-e (mind `planned`); a pontos descriptor-mező, amelyből az identitás olvasódik (a V08 implementációs specifikálásakor rögzítendő); a `module_identity_mismatch` azonosítót; a származtatott JSON Schema-t (nincs). A Relay és a base-repo implementáció HOLD marad.
