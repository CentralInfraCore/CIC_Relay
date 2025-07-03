# Plugin interfész a CIC rendszerben

A CIC rendszer végrehajtási logikája plugineken keresztül valósul meg. A relay komponensek nem tartalmaznak konkrét üzleti logikát vagy állapotkezelést – minden műveletet a dinamikusan betöltött `.so` fájlokban definiált pluginfüggvények végeznek el. A séma deklarálja, **melyik plugin melyik függvényét** kell meghívni – a végrehajtás már a plugin felelőssége.

## A plugin szerepe

A plugin egy jól körülhatárolt, stateless, tiszta függvényhalmaz, amely:

* a bemeneti állapotleírás alapján műveletet hajt végre,
* visszajelzést ad (siker, hiba, aktuális állapot),
* nem tárol adatot, nem lép kapcsolatba más komponensekkel,
* izoláltan, csak a relay hívására működik.

## Kötelező függvények (interfészek)

Egy plugin akkor használható a CIC-ben, ha az alábbi függvények közül egyet vagy többet exportál:

### `ApplyState(input []byte) ([]byte, error)`

Állapot végrehajtása – tipikusan konfigurációk, változások, telepítések esetén.

### `ObserveState(input []byte) ([]byte, error)`

Megfigyelés – az aktuális állapot visszajelzése egy strukturált objektumként (pl. JSON).

### `Notify(input []byte) error`

Riasztás, eseményküldés – webhook, log vagy más értesítés plugin oldalról.

### `Identify() (string, string, string)`

Azonosító – a plugin neve, verziója és típusa (pl. `prometheus-agent`, `1.0.0`, `agent`).
Ez kötelező minden modulnál a regisztrációhoz.

## Technikai követelmények

* A plugin `go build -buildmode=plugin` opcióval épül
* Minden `.so` fájl egy plugin modult tartalmaz
* Exportált függvények pontos szignatúrával rendelkeznek
* A relay `plugin.Open()` + `Lookup()` segítségével tölti be őket

## Példakód (Go)

```go
func ApplyState(input []byte) ([]byte, error) {
    var cfg map[string]string
    if err := json.Unmarshal(input, &cfg); err != nil {
        return nil, err
    }
    // konfiguráció alkalmazása itt
    return []byte(`{"result":"ok"}`), nil
}
```

## Plugin korlátai (tudatosan)

* ❌ Nem tarthat fenn saját állapotot (sem globálisan, sem lemezen)
* ❌ Nem kommunikálhat más pluginokkal vagy relékkel közvetlenül
* ❌ Nem férhet hozzá külső forrásokhoz, hacsak az inputban nincs deklarálva

Ez biztosítja a teljes determinizmust, auditálhatóságot és újrafuttathatóságot.

## Következő olvasmány

* [`schema_kezeles.md`](./schema_kezeles.md): hogyan deklarálja a séma, hogy mit kell végrehajtani
* [`graf_vegrehajtas.md`](./graf_vegrehajtas.md): hogyan épül be a plugin a relay műveleti gráfjába
