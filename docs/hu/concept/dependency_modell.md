# Függőségi modell a CIC rendszerben

A CIC rendszer deklaratív sémáinak egyik kulcsfontosságú mezője a `Dependencies`, amely meghatározza, hogy egy adott művelet végrehajtása csak akkor történhet meg, ha bizonyos más objektumok már „kész” állapotban vannak. Ez a megközelítés biztosítja, hogy a rendszer egésze determinisztikusan és logikusan haladjon előre.

## Két szintű feltételrendszer

A futtathatóságot két mező befolyásolja:

* `Dependencies` – más objektumok szükséges állapota (pl. `host`, `network`, `service.*`)
* `StateRequirement` – konkrét kulcs-érték feltételek az állapottérből (pl. `host.state = alive`)

Ezek kombinálása lehetővé teszi:

* strukturális függőségek (objektumközi kapcsolat)
* tényleges állapotvizsgálat (az adott séma saját feltételei)

## Mikor tekinthető teljesültnek egy dependency?

Egy `Dependency` akkor tekinthető teljesültnek, ha az adott objektum:

* már létrejött (létezik egy relay által értelmezett séma hozzá)
* állapota megfelel a saját sémájában előírt kritériumoknak (pl. `ApplyState` sikeres, `ObserveState` szerint működőképes)

A „készre jelentés” tehát **nem fix szabály alapján történik**, hanem a séma és plugin visszajelzése alapján.

## Példa

```yaml
Dependencies:
  - host
  - host.services.postgresql
StateRequirement:
  host.state: alive
  host.services.postgresql: running
```

Ebben a példában a séma csak akkor hajtható végre, ha a `host` és `postgresql` már deklarált és működőképes.

## Függőségek és gráf

A `Dependencies` információ önmagában nem írja át a gráf szerkezetét, de **kiváltja vagy blokkolja** a `NextHop` mezőből származó lépések indítását. Tehát a relay nem halad tovább, ha a dependency-k nem teljesülnek.

## Következő olvasmány

* [`graf_vegrehajtas.md`](./graf_vegrehajtas.md): hogyan halad előre a relay a gráf mentén
* [`allapotmodell.md`](./allapotmodell.md): hogyan értelmezzük, hogy egy objektum „kész”
