# Függőségi modell a CIC rendszerben

A CIC rendszer deklaratív működésének kulcseleme a `Dependencies` mező, amely meghatározza, hogy egy adott műveleti séma **csak akkor** hajtható végre, ha más objektumok már elérték a működőképes (azaz „kész”) állapotot. Ez a modell biztosítja a rendszer **determinálható**, **időben rendezett** és **állapotalapú** végrehajtását.

## Két szintű feltételrendszer

A relay végrehajtási logikája két típusú feltételt értékel:

* `Dependencies` – más objektumokra való hivatkozás, amelyeknek teljesíteniük kell a saját állapotelvárásaikat
* `StateRequirement` – lokális állapotfeltételek a jelenlegi sémán belül

> A `Dependencies` más entitásokra utal, míg a `StateRequirement` a saját objektumra vonatkozó konkrét állapotot vár el.

## Mikor tekinthető egy dependency teljesültnek?

Egy függőség akkor minősül teljesültnek, ha:

* **a hivatkozott objektum létezik** (pl. deklarálva van egy sémában, és a relay értelmezni tudja),
* **az objektum állapota megfelel** a saját séma `StateRequirement` mezőjének,
* **a kapcsolódó plugin `ObserveState()` visszajelzése alapján** az objektum „késznek” tekinthető.

Ez azt jelenti, hogy a relay nem „emlékszik” a teljesítésre, hanem minden esetben újra kiértékel.

## Példa dependency-re és állapotra együtt

```yaml
Dependencies:
  - host
  - host.services.postgresql
StateRequirement:
  host.state: alive
  host.services.postgresql: running
```

Ebben az esetben a séma csak akkor kerül végrehajtásra, ha a `host` és `host.services.postgresql` objektumok:

* léteznek,
* a plugin `ObserveState()` szerint működő állapotban vannak,
* és a `StateRequirement` mezőben megadott értékek egyeznek a lekért állapottal.

## Hatása a gráfra és a NextHop működésre

A `Dependencies` **nem módosítja a végrehajtási gráf topológiáját**, de hatással van annak aktiválására:

* Ha egy dependency **nem teljesül**, a relay **nem hajtja végre** az adott sémát
* Ez blokkolhatja a `NextHops` lépéseit is, így elágazások vagy párhuzamos ágak is feltartódhatnak

Ez a mechanizmus lehetővé teszi:

* szigorú sorrend biztosítását (pl. VM → szolgáltatás)
* feltételes futtatást (csak ha más elem működik)
* akár környezetfüggő végrehajtási logikát is

## Reláció az állapotmodellel

A `Dependencies` mező **csak akkor értelmezhető**, ha létezik egy állapotmodell, amely alapján az objektum „készre jelentett”. Ezért a dependency és az állapotlogika **szoros kapcsolatban áll**.

> A `Dependencies` mindig más entitás „készre jelentésére” támaszkodik, nem a saját séma dönt róla.

## Következő olvasmány

* [`graf_vegrehajtas.md`](./graf_vegrehajtas.md): hogyan halad előre a relay a gráf mentén
* [`allapotmodell.md`](./allapotmodell.md): hogyan értelmezzük, hogy egy objektum „kész”
