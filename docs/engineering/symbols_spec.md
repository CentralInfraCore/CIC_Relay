# Specifikáció: Publikus Szimbólumok Szinkronizálása

## 1. Cél és Motiváció

Ez a dokumentum a `context/SYMBOLS.md` fájl karbantartásának és automatikus ellenőrzésének szabályait rögzíti. A cél egy olyan CI-kapu (gate) létrehozása, amely biztosítja, hogy a projekt publikus API-ját leíró dokumentáció mindig naprakész és szinkronban van a tényleges kóddal. Ez megelőzi a "dokumentáció-driftet", és garantálja, hogy a projekt felhasználói és fejlesztői mindig a valós állapotot tükröző API-leírással dolgozhatnak.

## 2. A Publikus API Hatóköre (Scope)

Egy szimbólum akkor számít a projekt publikus API-ja részének, ha a következő feltételek mindegyike teljesül:

1.  **Exportált:** A szimbólum neve nagy kezdőbetűvel kezdődik.
2.  **Engedélyezett csomagban van:** A szimbólumot tartalmazó csomag szerepel a `context/SYMBOLS.ALLOWPKGS` fájlban.

### `context/SYMBOLS.ALLOWPKGS` Fájl

Ez a fájl egy egyszerű, soronként egy csomagot tartalmazó lista, amely meghatározza a publikus API hatókörébe tartozó Go csomagokat. Példa:

```
centralrelay/core/cabinet
```

## 3. Kanonikus Formátum

A `context/SYMBOLS.md` fájl tartalma egy determinisztikus, kanonikus formátumot követ. A szimbólumokat a `tools/symbolsgen` eszköz generálja a következő struktúra szerint:

- **Rendezés:** A szimbólumok szigorúan ábécérendben vannak, a következő hierarchia szerint:
    1. Csomag neve (pl. `centralrelay/core/cabinet`)
    2. Szimbólum típusa (`const`, `func`, `type`, `var`)
    3. Szimbólum neve (pl. `NewAPI`)

- **Formátum:** Minden szimbólum a teljes, kvalifikált Go-szignatúrájával szerepel, beleértve a receivert, paramétereket és visszatérési típusokat.

## 4. Szűrési Politika (Kivételek)

A szimbólumgyűjtő eszköz a következőket automatikusan figyelmen kívül hagyja:

- `_test.go` végződésű fájlok.
- `internal/` könyvtárban található csomagok.
- Olyan fájlok, amelyeket build-tagek (pl. `//go:build ignore`) kizárnak a standard buildből.

## 5. Frissítési Folyamat

Ha egy fejlesztő módosítja a publikus API-t (új exportált szimbólumot ad hozzá, meglévőt töröl vagy módosít egy engedélyezett csomagban), a `context/SYMBOLS.md` fájlt frissítenie kell.

Ezt a következő paranccsal teheti meg:

```sh
go run ./tools/symbolsgen
```

A CI-folyamat automatikusan ellenőrzi, hogy a `context/SYMBOLS.md` szinkronban van-e a kóddal. Ha eltérést talál, a build sikertelen lesz, jelezve a fejlesztőnek a frissítés szükségességét.
