# Plugin interfész a CIC rendszerben

A CIC rendszerben a műveletek végrehajtását a relay komponensek nem maguk hajtják végre, hanem dinamikusan betöltött plugin modulokon keresztül. Ezek a pluginok `.so` fájlformátumban vannak jelen, és meghatározott függvényeket kell exportálniuk. A séma csak azt határozza meg, **melyik plugin melyik függvényét kell meghívni** – az implementáció részleteit maga a plugin tartalmazza.

## A plugin szerepe

A plugin egy imperatív végrehajtási egység, amely:

* egy adott séma műveletét végrehajtja (`ApplyState`, `ObserveState`, stb.)
* csak a neki átadott adatból dolgozik
* semmit nem tárol vagy feltételez a teljes rendszer működéséről

## Kötelező interfészek

A rendszer minden pluginjától az alábbi függvények legalább egyikének megléte elvárt (a plugin típusától függően):

### `ApplyState(input []byte) ([]byte, error)`

A megadott állapotot végrehajtja. Általában deklaratív → imperatív átalakítás.

### `ObserveState(input []byte) ([]byte, error)`

Megfigyeli a jelenlegi állapotot és visszaadja. Eredménye `map[string]string` vagy hasonló.

### `Notify(input []byte) error`

Egy állapotváltozás vagy esemény közlése (pl. webhook vagy log).

### `Identify() (string, string, string)`

A plugin azonosító metaadatai: név, típus, verzió. A relay ezt használja `LibraryMeta` feltöltéshez.

## Technikai elvárások

* A plugin `go build -buildmode=plugin` móddal épül
* Egy `.so` fájl csak egy plugin modult tartalmaz
* Export függvényeknek pontosan kell egyezniük
* A rendszer a `plugin.Open()` és `Lookup()` Go-függvényeket használja a betöltéshez

## Plugin működési példa

```go
func ApplyState(input []byte) ([]byte, error) {
    var req ConfigRequest
    if err := json.Unmarshal(input, &req); err != nil {
        return nil, err
    }
    // Végrehajtás logika itt
    return []byte(`{"result":"ok"}`), nil
}
```

## További szabályok

* A plugin csak a paraméterként kapott adatot használhatja
* Nem tárolhat állapotot sem globálisan, sem lemezen
* Nem kommunikálhat közvetlenül más pluginokkal vagy relékkel

## Következő olvasmány

* [`dependency_modell.md`](./dependency_modell.md): hogyan értékeljük ki a séma futtathatóságát
* [`graf_vegrehajtas.md`](./graf_vegrehajtas.md): hogyan történik a relay szintű végrehajtási folyamat
