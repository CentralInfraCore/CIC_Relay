# CIC rendszer – áttekintés

A CentralInfraCore (CIC) egy moduláris, plug-in-alapú infrastruktúra-végrehajtási rendszer, amely a deklaratív állapotleírást végrehajtási gráfokra fordítja, és azokat decentralizált relay komponenseken keresztül hajtja végre. A rendszer célja nem csupán egy eszköz biztosítása az infrastruktúra kezelésére, hanem egy elméletileg megalapozott és gépi értelmezésre is optimalizált kontrollmodell létrehozása.

## Általános cél

A CIC célja, hogy az infrastruktúra bármely elemének állapotát és konfigurációját deklaratívan lehessen megadni, majd ezt a definíciót egy vagy több relay komponens hajtsa végre, aszinkron módon, plugin-modulokon keresztül. A relay nem vezérlő komponens, hanem végrehajtó végpont.

## Fő jellemzők

* **Deklaratív működés**: az elvárt állapot YAML-leírásban jelenik meg
* **Plug-in architektúra**: a műveleti logika `.so` fájlokban definiálható
* **Relay végrehajtás**: nincs központi vezérlés, a relay csak végrehajt
* **Gráfalapú működés**: a végrehajtás lépésről lépésre, műveleti gráf alapján történik
* **Állapot-feltételes működés**: minden lépés csak akkor fut, ha a függőségei teljesülnek

## A Relay szerepe

A relay a CIC rendszer „idegvégződése” – nem dönt, nem tárol, nem vezérel. Feladata:

1. **Sémák értelmezése**: meghatározza, milyen műveletet kell végrehajtani
2. **Függőségek kiértékelése**: megnézi, teljesültek-e a `Dependencies` vagy `StateRequirement` mezők
3. **Plugin meghívása**: ha kell, betölti a megfelelő `.so` modult és meghívja a benne lévő függvényt
4. **Következő lépések kiszámítása**: `NextHops` alapján továbbhalad

## További dokumentáció

A rendszer részletes architektúráját, objektumtípusait és interfészeit a hivatalos dokumentáció tartalmazza:

👉 [https://github.com/CentralInfraCore/.github](https://github.com/CentralInfraCore/.github)

## Következő lépések a dokumentációban

* [`relay_pozicionalas.md`](./relay_pozicionalas.md): a relay komponens részletes bemutatása
* [`schema_kezeles.md`](./schema_kezeles.md): hogyan működik a séma, milyen mezők vannak
* [`plugin_interface.md`](./plugin_interface.md): mit vár el a rendszer egy `.so` modultól
* [`dependency_modell.md`](./dependency_modell.md): hogyan kezeljük a függőségi viszonyokat
