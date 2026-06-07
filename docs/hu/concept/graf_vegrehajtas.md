# Gráf-alapú végrehajtás a CIC relay-ben

A CIC rendszer végrehajtási logikája egy deklarált, sémákból felépülő **irányított gráf** mentén történik. Ez a gráf határozza meg, hogy egy adott relay milyen műveleti sémákat hajtson végre, milyen sorrendben, milyen függőségek teljesülése esetén.

A relay minden végrehajtási folyamatot **determinált módon** kezel: nem dönt önállóan, hanem kizárólag a `ExecutionGraph`, a séma definíciók és az aktuális állapot alapján működik.

## A végrehajtási gráf felépítése

Egy `ExecutionGraph` az alábbi elemekből áll:

```yaml
ExecutionGraph:
  Name: example-setup
  Steps:
    - init-host
    - configure-network
    - install-services
```

A `Steps` mező lineáris sorrendet definiál, de minden lépés saját séma alapján határozza meg, hogy:

* milyen állapotot vár el (`StateRequirement`),
* milyen függőségeknek kell teljesülniük (`Dependencies`),
* mikor és hová lép tovább (`NextHops`).

## A végrehajtás menete

1. A relay betölti a hozzárendelt `ExecutionGraph` definíciót.
2. A `Steps` mező alapján feldolgozza az egyes sémákat.
3. Minden sémánál:

   * Kiértékeli a `Dependencies` és `StateRequirement` mezőket.
   * Ha teljesülnek, meghívja a megfelelő plugin függvényt (`ApplyState`, `ObserveState`, stb.).
   * A `NextHops` mező alapján azonosítja a következő művelete(ke)t.
4. Async módban folytatódhat több irányban is, ha a gráf ezt lehetővé teszi.

## Aszinkron ágkezelés

A relay nem lineáris queue-ként működik, hanem lehetősége van **ágankénti elágazásra és párhuzamos végrehajtásra**:

* A `NextHops` lehet több célpontból álló lista
* Regex-alapú vagy struktúrált `NextHop` minták is megadhatók (pl. `local.services.*`)
* Minden relay példány önállóan, de azonos szabályrendszer szerint hajt végre

Ezáltal:

* különböző szolgáltatások egyidejűleg telepíthetők,
* a relay nem blokkolja a többi ágat, ha egy sémára még várni kell,
* a párhuzamos futtatás determinisztikus marad, amíg a függőségek teljesülnek.

## Elágazás és értékelési logika

A `NextHops` mező lehet:

* konkrét sémanév (pl. `start-database`)
* leképezett kulcs (pl. `local.children` vagy `host.services.*`)
* függvényalapú kiértékelés (pl. `ExpandNextHop()` belső logika alapján)

Ez lehetővé teszi:

* logikai szétágazást anélkül, hogy a gráf explicit módon Y-alakú lenne
* többszörös példányosítást (pl. több szolgáltatásra párhuzamosan)
* dinamikus objektumhivatkozást (minták alapján)

## Determinizmus és ellenőrizhetőség

Minden relay példány ugyanazt az `ExecutionGraph` + `SchemaSet` + állapot snapshot alapján hajtja végre a folyamatot.
Ez garantálja:

* reprodukálhatóságot (ugyanabból ugyanaz lesz)
* validálhatóságot (külső rendszer visszaellenőrizheti)
* transzparenciát (minden lépés előre jelezhető)

## Következő olvasmány

* [`plugin_interface.md`](./plugin_interface.md): hogyan hajtanak végre a relay által hívott pluginek
* [`allapotmodell.md`](./allapotmodell.md): hogyan értelmezzük az állapotot futtatás előtt
