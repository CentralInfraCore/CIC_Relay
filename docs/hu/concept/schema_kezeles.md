# Séma-kezelés a CIC rendszerben

A CIC rendszer központi működési egysége a séma (`SchemaDefinition`), amely meghatározza, hogy mit, mikor és milyen feltételekkel kell végrehajtani. A séma nem kódot tartalmaz, hanem egy deklarációs objektum, amelyet a relay értelmez és a megfelelő plugin-modulon keresztül végrehajt.

## Séma szerepe

Egy séma nem más, mint egy absztrakt leírás:

* **Mit kell végrehajtani** – plugin hívás típusa (pl. `ApplyState`, `ObserveState`)
* **Mikor** – feltételek (`Dependencies`, `StateRequirement`)
* **Hogyan tovább** – `NextHops` mezők definiálása

A séma egy sorozatban (`ExecutionGraph`) jelenik meg, amely lépésről lépésre kerül feldolgozásra a relay által.

## A séma mezői

| Mező               | Típus              | Leírás                                                                          |
| ------------------ | ------------------ | ------------------------------------------------------------------------------- |
| `Name`             | string             | A séma egyedi neve a gráfon belül                                               |
| `PluginRef`        | string             | A hivatkozott plugin azonosítója (név/verzió)                                   |
| `Function`         | string             | A pluginon belüli meghívandó függvény neve                                      |
| `Dependencies`     | \[]string          | Milyen más objektum(ok) kell(ene), hogy teljesüljenek                           |
| `StateRequirement` | map\[string]string | Kulcs-érték alapú elvárás az állapottérben                                      |
| `NextHops`         | \[]string          | A következő sémák (vagy `local.*::relay`, `service.*` regex alapú hivatkozások) |

## Állapottal való kapcsolat

A `StateRequirement` mező határozza meg, hogy az adott séma mikor tekinthető futtathatónak. Például:

```yaml
StateRequirement:
  host.state: alive
  host.services.psql: running
```

Ha bármelyik feltétel nem teljesül, a relay nem hajtja végre a sémát.

## NextHop működése

A `NextHops` mező nem konkrét sémanevet vár mindig, hanem lehetőséget ad:

* Statikus hivatkozásra: `setup-networking`
* Regex/absztrakcióra: `local.*::relay`, `service.*`
* Függvényalapú kiterjesztésre (pl. `ExpandNextHops()` belső logika)

Ezzel a relay async módon tovább tud haladni több ágon is, a konkrét objektumok függvényében.

## Séma és plugin kapcsolata

A séma csak deklarálja, hogy **melyik plugin** melyik **függvényét** kell meghívni. Maga a plugin `.so` fájl formájában van jelen, és dinamikusan kerül betöltésre a futtatás során.

Ez biztosítja a **szigorú szétválasztást a vezérlés (schema) és végrehajtás (plugin) között**.

## Következő olvasmány

* [`plugin_interface.md`](./plugin_interface.md): hogyan kell egy pluginnak viselkednie
* [`dependency_modell.md`](./dependency_modell.md): mikor tekintünk egy sémát futtathatónak
