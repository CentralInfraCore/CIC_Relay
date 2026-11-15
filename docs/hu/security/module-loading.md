# Modulbetöltés: aláírási szabályok

Egy WASM modul hármasként érkezik a relay-hez: `<név>.yaml` (a leíró), `<név>.wasm` és `<név>.pem` (az aláíró tanúsítványa). Ez a dokumentum azt mondja meg, mit tesz a relay az egyes kombinációkkal, és ebből mit állíthat az operátor. A betöltőnek két belépési pontja van (a binárisba ágyazott és a lemezre telepített modulok); mindkettő ugyanazon a függvényen megy át, ezért ugyanaz a szabály vonatkozik rájuk.

## 1. A szabály

A döntés két dologtól függ: a trust módtól és attól, hogy mi a `.pem` fájl.

| Eset | `prod` | `dev` (alapértelmezés) | `dev` + `CIC_ALLOW_UNSIGNED_MODULES=1` |
|---|---|---|---|
| nincs `.pem` fájl (aláíratlan) | **elutasítva** | **elutasítva** | engedett, **capability nélkül** |
| `.pem` van, érvényes lánc (EKU CodeSigning, CRL, frissesség) | engedett | engedett | engedett |
| `.pem` van, nem PEM tanúsítvány | **elutasítva** | **elutasítva** | **elutasítva** |
| `.pem` van, a tanúsítvány nem értelmezhető | **elutasítva** | **elutasítva** | **elutasítva** |
| `.pem` van, a lánc nem ellenőrizhető (nem megbízható, lejárt, visszavont, rossz felhasználás) | **elutasítva** | **elutasítva** | **elutasítva** |
| `.pem` van, de nincs betöltött trust store az ellenőrzéséhez | **elutasítva** | **elutasítva** | **elutasítva** |
| `.pem` létezik, de nem olvasható | **elutasítva** | **elutasítva** | **elutasítva** |

A `CIC_ALLOW_UNSIGNED_MODULES=1` `prod`-ban figyelmeztetéssel figyelmen kívül marad: a `prod` oszlop nem változik.

Az opt-in egyetlen dolgot dönt el: betölthet-e egy modul, amelynek **egyáltalán nincs aláírásfájlja**. Soha nem teszi elfogadhatóvá a jelen lévő, de érvénytelen aláírást. Ha az aláírás ott van, ellenőrizhetőnek kell lennie; a hibás formátumú, nem ellenőrizhető vagy nem olvasható aláírás hiba, nem „aláíratlan".

## 2. Az opt-in

| Változó | Értékek | Hatás |
|---|---|---|
| `CIC_ALLOW_UNSIGNED_MODULES` | beállítatlan, üres vagy `0`: ki. `1`: be (csak `dev`). Minden más (`true`, `yes`, ` 1`, ...) **indulási hiba** | Betölti az aláírásfájl nélküli modulokat. Indításkor és minden engedett modulnál figyelmeztetésként naplózva |

Elgépelés nem nyithat kivételt: csak a pontos `1` érték teszi, bármi más megállítja az indulást.

Az opt-in által engedett aláíratlan modul:

- **nem kap capabilityt.** Nincs a `git` vagy `cic-flow` registryhez kötve, ezért minden hívása ezekre a host modulokra tiltott (lásd [git-host-functions.md](git-host-functions.md));
- a WASM digestjét továbbra is ellenőrzi a leíróval szemben;
- az audit naplóban `outcome=allowed-unsigned` jelölést kap.

## 3. Operátori megjegyzések

- A relay alapértelmezése mindkét módban fail-closed. Aki `dev`-ben aláíratlan modulokat futtatott, vagy aláírja őket a fejlesztői CA-val, vagy a saját környezetében explicit beállítja az opt-int.
- Ne állítsd be a `CIC_ALLOW_UNSIGNED_MODULES`-t megosztott, hosszú életű vagy produkció közeli környezetben. `prod`-ban nincs hatása, de a beállítása csak elfedi, melyik környezet micsoda.
- Az opt-in nem ment meg egy olyan modult, amely alá volt írva, de az aláírása most elutasított (lejárt tanúsítvány, visszavont, nem megbízható CA). Írd alá újra.

## 4. Mi a host felület

Aláírva vagy sem, a betöltött modul csak azokat a host modulokat importálhatja, amelyeket a relay regisztrál: `git`, `cic-flow` és WASI. Egy teszt (`TestHostSurface_MatchesThePolicyTable`) a produkciós huzalozásból felsorolja őket, és minden függvényt egy átnézett táblához hasonlít. Elbukik, ha olyan függvény jelenik meg, amelyet a tábla nem osztályoz; ha egy osztályozott eltűnik; és ha egy host modul olyan módon regisztrálódik, amelyet a felsorolás nem lát. Egy új host függvényhez ezért tudatos, átnézett módosítás kell a táblában.

## 5. Viselkedésváltozások

A változtatás előtt az aláíratlan modul, az olyan modul, amelynek aláírásfájlja nem PEM tanúsítvány, és az aláírt modul trust store nélkül mind **betöltődött** (capability nélkül). Most elutasítottak `prod`-ban és alapértelmezésben `dev`-ben is. Ez a [GHSA-5489-jhgc-m947](https://github.com/CentralInfraCore/CIC_Relay/security/advisories/GHSA-5489-jhgc-m947) javításának második fele: a capability-kötés már fail-closed volt, mostantól maga a betöltés is az.
