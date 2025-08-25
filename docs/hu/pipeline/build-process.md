# Build folyamat útmutató

Ez a dokumentum a Relay projekt build folyamatát írja le, lépésről lépésre.

## 1. Előkészítés
- Függőségek letöltése (`go mod download`).
- Modul cache előkészítése.
- Tesztkörnyezet és szükséges konténerek inicializálása.

## 2. Forráskód ellenőrzés
- Commit aláírás ellenőrzése (`git verify-commit`).
- Kódminőség ellenőrzése (lint, staticcheck, ineffassign).
- Tesztek futtatása (unit és integrációs tesztek).

## 3. Fordítás
- Bináris build a commit hash és build metaadat beágyazásával.
- RootCA aláírás hozzáadása a metaadatokhoz.

## 4. Artefaktum kezelés
- Bináris mentése verzió és commit hash alapján mappastruktúrába.
- Külső YAML meta fájl generálása ellenőrzéshez.

## 5. Értesítés és publikálás
- MQ értesítés a build eredményéről.
- Artefaktumok továbbítása a validációs és tesztkörnyezetekbe.

## 6. Ellenőrzési pontok
- Metaadat és bináris konzisztencia ellenőrzése.
- CA validáció (CRT, CRL, OCSP opció).
- Eredmények logolása és archiválása audit célra.
