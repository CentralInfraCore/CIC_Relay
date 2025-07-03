# Gráf-alapú végrehajtás a CIC relay-ben

A CIC relay komponensei nem véletlenszerű vagy állandó sorrendben hajtanak végre műveleteket, hanem egy deklaratívan leírt, determinisztikus műveleti gráf mentén. Ez a gráf (`ExecutionGraph`) határozza meg, milyen sémákat kell feldolgozni, milyen sorrendben, és milyen elágazásokkal.

## Gráf felépítése

A gráf egy sor sémanév (vagy sémacímke), amelyek sorrendje és kapcsolata a YAML definícióban jelenik meg. A séma definíciók egymásra hivatkozhatnak `NextHops` mezőn keresztül.

```yaml
ExecutionGraph:
  Name: setup-cluster
  Steps:
    - create-host
    - install-services
    - configure-network
```

A `Steps` lista lineárisan írja le a sorrendet, de a `NextHops` mezők segítségével a relay elágazásokat is képes kezelni.

## Végrehajtási logika

1. **Indulás**: A relay betölti az adott nevű gráfot
2. **Séma-feldolgozás**: Sorra végigmegy az adott `Steps` mentén
3. **Dependency és állapot ellenőrzés**: Minden séma csak akkor hajtható végre, ha:

    * a `Dependencies` teljesültek
    * a `StateRequirement` igaz
4. **Plugin meghívás**: Meghívja a séma által hivatkozott plugin függvényt
5. **NextHop kiterjesztés**: A `NextHops` mező alapján továbblép (akár async is)

## Aszinkron működés

A relay nem vár meg minden korábbi lépést, ha a függőségek teljesülnek, a `NextHops` mentén akár párhuzamos ágak is elindulhatnak.

Ez lehetővé teszi:

* szolgáltatások egyidejű telepítését
* host-szintű és cluster-szintű műveletek elkülönítését
* relay-ek közti paralel működést

## Determinizmus és validáció

A gráf futása determinisztikus, ha:

* a séma definíciók egyértelműek
* nincs rejtett vagy külső függőség
* a pluginok tisztán és állapotmentesen működnek

## Következő olvasmány

* [`allapotmodell.md`](./allapotmodell.md): hogyan mérhető az „elért állapot”
