# Séma kezelés a CIC rendszerben

A CIC végrehajtási logikájának alapegysége a **séma**, amely egy objektum vagy művelet deklarációja. A relay nem „értelmez” programkódot vagy állapotgépet – minden végrehajtás egy sémán alapul, amely előírja, **mit kell végrehajtani, milyen állapotban, milyen feltételekkel**.

## A séma fő mezői

```yaml
Schema:
  Name: host-init
  PluginRef: host/init@v1.0.0
  StateRequirement:
    host.state: init
  Dependencies:
    - infra.network
  NextHops:
    - host.services.*
```

### `Name`

A séma egyedi azonosítója – ez alapján hivatkoznak rá a gráfok, más sémák, vagy relay-ek.

### `PluginRef`

A meghívandó plugin referencia. Tartalmazhat nevet, verziót, típust. Pl.: `package/function@verzió` vagy `host/init@v1.0.0`.

### `StateRequirement`

A relay csak akkor hajtja végre a sémát, ha ezek a kulcs-érték állapotok teljesülnek. A kulcsok `ObserveState()` által visszaadott mezőkre vonatkoznak.

### `Dependencies`

Más sémákra, objektumokra való függőség. Csak akkor lép életbe ez a séma, ha a hivatkozott objektumok „kész” állapotba kerültek.

### `NextHops`

Következő műveleti egységek. Ezek lehetnek konkrét sémanevek, minták (pl. `service.*`), vagy strukturált mezők (pl. `local.services`). A relay ezt a mezőt elemzi aszinkron ágak indításához.

## A séma célja

* Műveleti szándék és feltételek egy helyen
* Plugin-hívás és továbbhaladás vezérlése
* Függőségek kiértékelése és validáció

> A séma nem végrehajtási utasítás, hanem deklaráció – a relay és plugin együttműködése hozza létre a végrehajtást.

## Belső viselkedés

A relay minden séma esetében:

* kiértékeli a `StateRequirement` mezőt a plugin válasza alapján,
* ellenőrzi a `Dependencies`-t,
* ha minden teljesül, meghívja a `PluginRef`-hez tartozó plugin függvényt,
* a `NextHops` mező szerint folytatja a végrehajtást.

## Séma mint kapu – és nem script

A CIC szemantikában a séma nem végrehajtandó „program”, hanem egy **deklaratív műveleti csomópont**, amely megmondja:

* mit vár el a rendszerállapottól,
* mit kell meghívni (plugin),
* mi történik, ha kész vagyunk (next).

## Következő olvasmány

* [`plugin_interface.md`](./plugin_interface.md): hogyan épül fel egy plugin
* [`dependency_modell.md`](./dependency_modell.md): mikor tekinthető egy séma futtathatónak
