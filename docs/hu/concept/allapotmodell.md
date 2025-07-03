# Állapotmodell a CIC rendszerben

A CIC rendszer működésének egyik legfontosabb alapja az állapotok értelmezése és felhasználása. Mivel a relay komponensek nem tárolnak állapotot, minden állapottal kapcsolatos értékelés külső megfigyelés (plugin) alapján történik. Az állapotmodell tehát nem adatbázis, hanem **aktív lekérdezésen és deklarált elváráson** alapuló működési logika.

## Állapot típusok

| Állapottípus       | Forrás                          | Példa                     |
| ------------------ | ------------------------------- | ------------------------- |
| Elvárt állapot     | Séma `StateRequirement` mezője  | `host.state = alive`      |
| Megfigyelt állapot | Plugin `ObserveState()` válasza | `{"host.state": "alive"}` |

A relay az elvárt és a megfigyelt állapotot nem tárolja, hanem minden esetben **új lekérdezéssel** vagy **átadott adatlappal** dolgozik.

## Hogyan értelmezzük az állapotot?

* A `StateRequirement` deklarálja, milyen állapotban kell lennie egy vagy több objektumnak
* Az `ObserveState()` pluginfüggvény visszaadja az aktuális állapotot
* A relay ezek között **egyszerű kulcs-érték egyezést** vizsgál

Ez azt jelenti, hogy nincs logikai diff, nincs `state history`, csak az aktuális snapshot számít.

## Mi nem része az állapotmodellnek?

* Nincs tárolt diff vagy rollback
* Nincs időbeli kontextus (nem vizsgálunk „előtte-utána” állapotot)
* Nincs saját adatbázis – minden input kívülről jön

## Mikor tekintünk egy objektumot „késznek”?

Ha:

* a `StateRequirement` teljesül
* a hozzá tartozó plugin sikeres választ adott (pl. `ObserveState`, `ApplyState`)
* a függőségeit már feldolgoztuk (lásd: dependency modell)

Ez a „kész” állapot **dinamikusan értékelt**, nem relészinten kerül bejegyzésre.

## Példa

```yaml
StateRequirement:
  host.state: alive
  service.prometheus: running
```

```json
{
  "host.state": "alive",
  "service.prometheus": "running"
}
```

Ha a plugin ezt visszaadja, a relay ezt „kész”-nek tekinti.

## Következő olvasmány

* [`dependency_modell.md`](./dependency_modell.md): hogyan kapcsolódik az állapot a függőségekhez
* [`graf_vegrehajtas.md`](./graf_vegrehajtas.md): hogyan halad a relay az állapotalapú értékelés mentén
