# Modulbetöltés: aláírási szabályok

Egy WASM modul hármasként érkezik a relay-hez: `<név>.yaml` (a leíró), `<név>.wasm` és `<név>.pem` (az aláíró **tanúsítványa**; nem a modul feletti aláírás). A #162 óta ehhez jön a release-burok: `<név>.envelope.json` (`ModuleReleaseEnvelope`, `CICMODENVv1`) és annak leválasztott aláírása, `<név>.sig` (7. szakasz). Ez a dokumentum azt mondja meg, mit tesz a relay az egyes kombinációkkal, és ebből mit állíthat az operátor. A betöltőnek két belépési pontja van (a binárisba ágyazott és a lemezre telepített modulok); mindkettő ugyanazon a függvényen megy át, ezért ugyanaz a szabály vonatkozik rájuk.

## 1. Mit ellenőriz a betöltő, és mit nem

A hétköznapi nyelv két különböző dolgot hív „aláírtnak"; a betöltő csak az elsőt végzi el.

| Ellenőrzés | Betöltéskor | Mit bizonyít |
|---|---|---|
| A `.pem` **tanúsítványlánca**: megbízható gyökér, EKU CodeSigning, érvényesség, CRL | **igen** | a modulhoz egy megbízható CA által kódaláírásra kiállított tanúsítvány tartozik |
| A WASM digestje és mérete a `.yaml` leíróval szemben | **igen** | a `.wasm` az, amit a leíró megnevez |
| **Kriptográfiai aláírás a modul felett** (nyers és futtatott WASM + a teljes leíró), a tanúsítvány kulcsával | **igen, ha a modul release-burkot hordoz** (#162, 7. szakasz) | a tanúsítvány birtokosa, akinek az operátor `module:sign` jogosultságot adott, pontosan ezt az artifactot és manifestet hagyta jóvá |

Burok nélkül a betöltő nem köti a tanúsítványt az artifacthoz; az ilyen modul `transitional`, és **capabilityt nem kap** (7. szakasz). Ahol ez a dokumentum vagy a README „aláírtat" mond burok nélküli modulra, olvasd így: „olyan tanúsítványt hordoz, amelynek lánca ellenőrizhető".

## 2. A szabály

A döntés két dologtól függ: a trust módtól és attól, hogy mi a `.pem` fájl.

| Eset | `prod` | `dev` (alapértelmezés) | `dev` + `CIC_ALLOW_UNSIGNED_MODULES=1` |
|---|---|---|---|
| nincs `.pem` fájl (aláíratlan) | **elutasítva** | **elutasítva** | engedett, **capability nélkül** |
| `.pem` van, a tanúsítványlánc érvényes (EKU CodeSigning, CRL, frissesség) | engedett | engedett | engedett |
| `.pem` van, nem PEM tanúsítvány | **elutasítva** | **elutasítva** | **elutasítva** |
| `.pem` van, a tanúsítvány nem értelmezhető | **elutasítva** | **elutasítva** | **elutasítva** |
| `.pem` van, a lánc nem ellenőrizhető (nem megbízható, lejárt, visszavont, rossz felhasználás) | **elutasítva** | **elutasítva** | **elutasítva** |
| `.pem` van, de nincs betöltött trust store az ellenőrzéséhez | **elutasítva** | **elutasítva** | **elutasítva** |
| `.pem` létezik, de nem olvasható | **elutasítva** | **elutasítva** | **elutasítva** |

A `CIC_ALLOW_UNSIGNED_MODULES=1` `prod`-ban figyelmeztetéssel figyelmen kívül marad: a `prod` oszlop nem változik.

Az opt-in egyetlen dolgot dönt el: betölthet-e egy modul, amelynek **egyáltalán nincs aláírásfájlja**. Soha nem teszi elfogadhatóvá a jelen lévő, de érvénytelen aláírást. Ha a tanúsítvány ott van, ellenőrizhetőnek kell lennie; a hibás formátumú, nem ellenőrizhető vagy nem olvasható tanúsítvány hiba, nem „aláíratlan". (Az ellenőrzés a tanúsítványláncra vonatkozik; hogy mit nem fed, az az 1. szakaszban áll.)

## 3. Az opt-in

| Változó | Értékek | Hatás |
|---|---|---|
| `CIC_ALLOW_UNSIGNED_MODULES` | beállítatlan, üres vagy `0`: ki. `1`: be (csak `dev`). Minden más (`true`, `yes`, ` 1`, ...) **indulási hiba** | Betölti az aláírásfájl nélküli modulokat. Indításkor és minden engedett modulnál figyelmeztetésként naplózva |

Elgépelés nem nyithat kivételt: csak a pontos `1` érték teszi, bármi más megállítja az indulást.

Az opt-in által engedett aláíratlan modul:

- **nem kap capabilityt.** Nincs a `git` vagy `cic-flow` registryhez kötve, ezért minden hívása ezekre a host modulokra tiltott (lásd [git-host-functions.md](git-host-functions.md));
- a WASM digestjét továbbra is ellenőrzi a leíróval szemben;
- az audit naplóban `outcome=allowed-unsigned` jelölést kap.

## 4. Operátori megjegyzések

- A relay alapértelmezése mindkét módban fail-closed. Aki `dev`-ben aláíratlan modulokat futtatott, vagy aláírja őket a fejlesztői CA-val, vagy a saját környezetében explicit beállítja az opt-int.
- Ne állítsd be a `CIC_ALLOW_UNSIGNED_MODULES`-t megosztott, hosszú életű vagy produkció közeli környezetben. `prod`-ban nincs hatása, de a beállítása csak elfedi, melyik környezet micsoda.
- Az opt-in nem ment meg egy olyan modult, amelynek tanúsítványa most elutasított (lejárt, visszavont, nem megbízható CA). Szerezz hozzá érvényes tanúsítványt.
- Az érvényes tanúsítvány önmagában nem hitelesíti a modul tartalmát: ezt a release-burok teszi (7. szakasz). Burok nélküli modul `transitional`, és nem kap capabilityt.

## 5. Mi a host felület

Aláírva vagy sem, a betöltött modul csak azokat a host modulokat importálhatja, amelyeket a relay regisztrál: `git`, `cic-flow` és WASI. Egy teszt (`TestHostSurface_MatchesThePolicyTable`) a produkciós huzalozásból felsorolja őket, és minden függvényt egy átnézett táblához hasonlít. Elbukik, ha olyan függvény jelenik meg, amelyet a tábla nem osztályoz; ha egy osztályozott eltűnik; és ha egy host modul olyan módon regisztrálódik, amelyet a felsorolás nem lát. Egy új host függvényhez ezért tudatos, átnézett módosítás kell a táblában.

## 6. Viselkedésváltozások

A változtatás előtt az aláíratlan modul, az olyan modul, amelynek aláírásfájlja nem PEM tanúsítvány, és az aláírt modul trust store nélkül mind **betöltődött** (capability nélkül). Most elutasítottak `prod`-ban és alapértelmezésben `dev`-ben is. Ez a [GHSA-5489-jhgc-m947](https://github.com/CentralInfraCore/CIC_Relay/security/advisories/GHSA-5489-jhgc-m947) javításának második fele: a capability-kötés már fail-closed volt, mostantól maga a betöltés is az.

## 7. Artifact-befogadás: a release-burok (#162)

A betöltő a `.pem` lánc-ellenőrzése **után**, a capability-kötés **előtt** ellenőrzi a release-burkot (`cmd/relay/module_admission.go`, `admitModuleArtifact`). Szerződés: O-1 `01-162-implementation-boundary.md` V1–V7, ENV-01–03.

**Egyetlen beolvasás.** A `.yaml` és a `.wasm` egyszer kerül beolvasásra; a hash-elt puffer az, amelyből a leíró parse-olódik, a capability-manifest kiolvasódik, és a tárolt, lefordított modul (`ModuleDescriptor.WasmCode`) épül. A hash és a példányosítás között cserélt fájl nem jut be.

| Lépés | Ellenőrzés | Hibakód |
|---|---|---|
| V1 | a burok és a `.sig` együtt van jelen; a burok sémája szigorú (ismeretlen mező, záró adat elutasítva); `envelope_version` = 1 | `ENVELOPE_MISSING`, `ENVELOPE_INVALID`, `ENVELOPE_VERSION` |
| V2 | `signer.alg` a tanúsítvány kulcstípusa (`ecdsa-p256-sha256` vagy `ed25519`; más elutasítva); `signer.key_id` = a tanúsítvány DER SPKI-jának sha256-ja; a `.sig` egy sor kanonikus base64 + LF; az aláírás a `CICMODENVv1` aláírt bájtokra érvényes | `ALG_MISMATCH`, `KEY_MISMATCH`, `SIG_ENCODING`, `SIGNATURE_INVALID` |
| V2b | az aláíró kulcsnak `module:sign` jogosultsága van az operátor pinelt kötéseiben (`CIC_MODULE_SIGN_AUTHORITIES`); a kulcs puszta jelenléte egy érvényes tanúsítványban nem jogosultság | `SIGNER_NOT_AUTHORIZED` |
| V3 | a tanúsítványlánc (2. szakasz) és a CICSourceCA provenance | a mai hibák; provenance nélkül `transitional` |
| V4 | `sha256(nyers WASM)` = `envelope.wasm_sha256` = `wasmDigest` = `metadata.buildHash` (`verifyBuildHash`); `sha256(ModuleDescriptor.WasmCode)` = `envelope.executed_wasm_sha256` | `WASM_DIGEST_MISMATCH`, `EXECUTED_DIGEST_MISMATCH` |
| V5 | `sha256(a .yaml nyers bájtjai)` = `envelope.manifest_digest` (a teljes fájl, kanonizálás nélkül) | `MANIFEST_DIGEST_MISMATCH` |
| V6 | nem hitelesített modul, amely `cic-flow`-t importál, `prod`-ban (és beállítatlan trust módban) elutasítva, nem capability nélkül betöltve | `MODULE_REJECTED` |
| V7 | a capability kulcsa `(module_id, envelope_digest)`, `envelope_digest = sha256(CICMODENVv1 aláírt bájtok)`; a futó példány ehhez kötött (`cic-flow` és `git` egyaránt) | — |

A `envelope.module_id` meg kell egyezzen a leíró `név@verzió` azonosítójával (`ENVELOPE_INVALID`); a verzió ezért szemver (`1.0.0`).

**Trust state** (az audit napló `trust_state` mezője): `authenticated` (V1–V7 teljesül; egyedül ez kap capabilityt), `transitional` (ellenőrzött tanúsítvány, burok vagy provenance nélkül; capability nélkül), `dev` (aláíratlan, dev opt-in; capability nélkül). Jelen lévő, de hibás burok vagy `.sig` **minden módban** elutasítás. A dev opt-in csak olyan modulra vonatkozik, amelynek se `.pem`-je, se burka, se `.sig`-je nincs; burok vagy `.sig` tanúsítvány nélkül `MODULE_REJECTED`.

**A `metadata.sign`** önálló producer-állítás marad: a relay nem ellenőrzi, a burok nem pótolja és nem pótolható vele; utólagos szerkesztése a manifest megváltozása (`MANIFEST_DIGEST_MISMATCH`).

**A transzformáció** (`wasm-canon/v1`) a meglévő `stripWasmModuleFunc` (wabin dekódolás, custom szekciók elhagyása, újrakódolás); a `Checksum()` változatlan. Felülírása (`SetStripWasmModuleFunc`) csak a `cic_test_seams` build taggel létezik; production buildben nem fordul le.

| Változó | Értékek | Hatás |
|---|---|---|
| `CIC_MODULE_SIGN_AUTHORITIES` | beállítatlan: senki nem jogosult (egy burok sem fogadható be). Fájl elérési út: `{"kind":"ModuleSignAuthorities","version":1,"authorities":[{"key_id":"sha256:…","roles":["module:sign"]}]}`. Olvashatatlan vagy hibás fájl (ismeretlen mező, szerep, duplikált vagy rossz formájú `key_id`): **indulási hiba** | az operátor pinelt `module:sign` kötései (V2b) |

**Viselkedésváltozás (#162):** a burok nélküli, csak tanúsítvánnyal ellátott modul többé nem kap `cic-flow` vagy `git` capabilityt (`transitional`), és `prod`-ban a `cic-flow`-t importáló ilyen modul elutasított. A production aktiválás a modulrepó release-folyamatának burok-előállításától (WP-01) függ.
