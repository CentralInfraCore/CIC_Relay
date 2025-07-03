# Relay pozicionálása a CIC rendszerben

A relay a CentralInfraCore (CIC) rendszerben egy speciális végrehajtó entitás, amely deklaratív sémaalapú logika mentén működik, de nem birtokol sem állapotot, sem vezérlőlogikát. Nem egy kontroller, hanem egy futtató. Minden relay egy konkrét végrehajtási egységet képvisel, amely az adott rendszer- vagy környezeti elemre vonatkozó sémák értelmezését végzi el.

## A relay célja

* **Dekódolás**: a relay értelmezi a `SchemaDefinition`-öket
* **Kiértékelés**: meghatározza, teljesülnek-e a `Dependencies` vagy `StateRequirement` feltételek
* **Végrehajtás**: meghívja a séma által meghatározott plugint (pl. `ApplyState`)
* **Továbbítás**: `NextHops` alapján aszinkron módon továbblép a következő séma felé

## A relay tulajdonságai

| Tulajdonság      | Leírás                                              |
| ---------------- | --------------------------------------------------- |
| Stateless        | Nem tárol előzményt vagy állapotot                  |
| Aszinkron        | A végrehajtás nem blokkol más ágakat                |
| Determinisztikus | Csak a séma, a plugin és az állapot alapján működik |
| Modularizált     | Több különálló relay működhet párhuzamosan          |

## Fő interfészei

A relay az alábbi műveleteket várja el a plugintól (például `.so` fájlokból):

* `ApplyState()` – egy állapot végrehajtása
* `ObserveState()` – aktuális állapot megfigyelése
* `Notify()` – események vagy állapotváltozások közlése
* `Identify()` – metaadat szolgáltatása (név, verzió, típus)

## Relay típusok

Bár maga a relay logikailag egységes, implementáció szerint több típus is kialakítható:

* **Lokális relay** – egy géphez vagy szolgáltatáshoz kapcsolódik
* **Átjáró relay** – külső rendszerekhez csatlakozik (pl. API gateway)
* **Aggregáló relay** – több gyermekállapotot egyesít (pl. `host -> services`)
* **Meta-relay** – diagnosztikai vagy elemző szerep (pl. coverage, logikaellenőrzés)

## Elhatárolás az orchestrátortól

A CIC-ben a relay **nem vezérlő**, hanem egy végrehajtási pont. A rendszer viselkedését:

* **a séma** határozza meg (deklaratív)
* **a plugin** végzi el (imperatív)
* **a relay** végrehajtja (passzív végpont)

Ez a három réteg tudatosan elválasztásra került, így a relay nem lép ki a saját hatásköréből.

## Következő olvasmány

* [`schema_kezeles.md`](./schema_kezeles.md): hogyan működik egy séma és annak mezői
* [`plugin_interface.md`](./plugin_interface.md): mit jelent a plugin és hogyan kapcsolódik be
