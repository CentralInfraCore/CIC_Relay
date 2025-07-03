# Relay komponens pozíciója a CIC rendszerben

A relay a CIC rendszer központi végrehajtó egysége. Feladata, hogy a deklarált séma-, gráf- és állapotleírások alapján meghatározza, **milyen műveletet kell végrehajtani, milyen sorrendben, milyen feltételekkel**, és mindezt **stateless, determinisztikus módon** végzi.

## A relay mint végrehajtó motor

A relay:

* nem tartalmaz állapotgépet,
* nem módosítja a sémákat vagy gráfokat,
* nem tárol adatot,
* nem tanul,
* nem hoz döntést.

Ehelyett:

* végrehajtja a deklarált műveleti gráfot,
* kiértékeli a függőségeket és állapotokat,
* pluginokon keresztül meghívja a konkrét műveleteket,
* továbbhalad a `NextHops` mezők alapján.

## A relay szerepe a rendszer architektúrájában

A relay **nem csak egy folyamat**, hanem egy újrafelhasználható végrehajtási egység:

* akár több példányban futtatható (pl. host-onként, cluster-szinten),
* minden példány ugyanazt az `ExecutionGraph` + `SchemaSet` + `PluginSet` kombinációt használja,
* minden példány determinisztikusan ugyanazokat a döntéseket hozza, ha az input állapot megegyezik.

## Mit nem csinál a relay

* ❌ nem hajt végre kódot közvetlenül
* ❌ nem ír fájlt, nem módosít konfigurációt
* ❌ nem tárol semmilyen belső állapotot
* ❌ nem old meg konfliktust vagy versenyhelyzetet

## Mikor van szükség relay-re?

Bármikor, ha:

* egy struktúrált műveleti gráfot kell végrehajtani
* pluginokra épülő, dinamikus végrehajtás szükséges
* egy központi „agent” kell, ami nem önhatalmú, hanem deklaratív sémák szerint dolgozik

## Példák a relay használatára

* VM-ek hálózatba illesztése (host → network)
* Szolgáltatások telepítése host-on belül
* Konfigurációk alkalmazása állapot alapján
* Aszinkron párhuzamos futtatás relay-példányonként

## Következő olvasmány

* [`cic_attekintes.md`](./cic_attekintes.md): hogyan illeszkedik a relay a teljes CIC architektúrába
* [`graf_vegrehajtas.md`](./graf_vegrehajtas.md): hogyan halad a relay a deklarált lépések mentén
