# Állapotmodell a CIC rendszerben

A CIC rendszer működésének egyik legfontosabb alapja az állapotok értelmezése és felhasználása. A relay komponensek **nem tárolnak sem állapotot, sem múltbeli változásokat**, hanem kizárólag **külső plugin által megfigyelt és visszajelzett állapotokra** reagálnak. Ezért a rendszer állapotmodellje nem adatbázis-alapú, hanem **deklaratív lekérdezésen** és **kontextuális megfeleltetésen** alapul.

## Állapottípusok és forrásuk

| Állapottípus       | Forrás                         | Példa                     |
| ------------------ | ------------------------------ | ------------------------- |
| Elvárt állapot     | Séma `StateRequirement`        | `host.state = alive`      |
| Megfigyelt állapot | Plugin `ObserveState()` válasz | `{"host.state": "alive"}` |

A relay minden esetben **stateless módon** működik: minden lekérdezés vagy művelet során új állapoti adatlapra támaszkodik.

## Állapotkiértékelés folyamata

1. A séma megadja az elvárt állapotot a `StateRequirement` mezőben
2. A relay lekéri az aktuális állapotot a plugin `ObserveState()` függvényével
3. A két állapotot kulcs-érték párok szerint összehasonlítja
4. Ha minden egyezés megtörtént, a séma „futtathatóvá” válik

> ⚠️ A relay semmit nem tárol, így nem tud diffelni vagy visszavonni: minden döntés **pillanatkép-alapú**

## Mi nem része a CIC állapotmodellnek?

* ❌ nincs állapottörténet (`state history`)
* ❌ nincs rollback vagy korábbi verzió visszaállítása
* ❌ nincs állandó cache vagy adatbázis

Minden művelet **aktuálisan megfigyelt állapot** alapján történik.

## Mit jelent, hogy egy objektum „kész”?

Egy objektum akkor tekinthető késznek, ha:

* a `StateRequirement` mezőben megadott összes feltétel teljesül
* a plugin `ObserveState()` visszajelzése ezt megerősíti
* az összes `Dependencies` feltétel kiértékelése sikeres (lásd: dependency modell)

Ez az állapot **nem globálisan kerül rögzítésre**, hanem **helyileg, műveletenként** kerül újraértelmezésre.

## Példák YAML és JSON formában

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

Ha a fenti JSON visszatérés megfelel a YAML elvárásnak, a relay végrehajtja a következő lépést.

## Következő olvasmány

* [`dependency_modell.md`](./dependency_modell.md): hogyan kapcsolódik az állapot a függőségekhez
* [`graf_vegrehajtas.md`](./graf_vegrehajtas.md): hogyan halad a relay az állapotalapú értékelés mentén
