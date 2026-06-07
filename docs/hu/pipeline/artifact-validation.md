# Artefaktum validációs útmutató

Ez a dokumentum bemutatja a Relay projekt build artefaktumainak validációs folyamatát.

## 1. Artefaktum típusok
- Binárisok (pl. `cic-relay`, `crt_parser`).
- Metaadat fájlok (YAML formátumban).
- Tanúsítványok (CRT) és CA ujjlenyomatok.

## 2. Validációs célok
- Bináris és metaadat konzisztencia.
- Forrás commit azonosítása és hitelességének ellenőrzése.
- RootCA aláírás érvényessége.

## 3. Validációs lépések
1. **Metaadat ellenőrzés**
    - Bináris `-v` kapcsolóval kinyert metaadat összehasonlítása a külső YAML fájllal.
2. **Hash ellenőrzés**
    - Bináris SHA-256 hash számítása és összevetése a meta fájlban található értékkel.
3. **Aláírás ellenőrzés**
    - Commit aláírásának ellenőrzése fejlesztői kulccsal.
    - RootCA aláírás ellenőrzése a CA publikus kulcsával.
4. **Tanúsítvány ellenőrzés**
    - CRT státusz ellenőrzése (CRL, OCSP).

## 4. Validációs kimenet
- **Sikeres ellenőrzés** → artefaktum hitelesített.
- **Sikertelen ellenőrzés** → hiba okának naplózása (commit, metaadat vagy aláírás eltérés).

## 5. Audit és archiválás
- Ellenőrzési eredmények logolása.
- Metaadatok és ellenőrzési naplók archiválása audit célra.