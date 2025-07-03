# CIC rendszer áttekintése

A Central Infrastructure Core (CIC) egy **declaratív állapotalapú infrastruktúra-végrehajtási modell**, amely lehetővé teszi, hogy bármely rendszer, szolgáltatás vagy környezet **sémaalapúan leírható**, és **determináltan kivitelezhető** legyen. A cél: a környezetek újrateremthetősége, auditálhatósága és aszinkron, skálázható működtethetősége.

## A rendszer logikai felépítése

A CIC négy rétegben működik:

1. **Leíró réteg**: YAML fájlokban leírt sémák, gráfok, állapotok, kapcsolatok
2. **Relay**: végrehajtó motor, amely plugineken keresztül hajtja végre a leírt műveleteket
3. **Plugin**: `.so` alapú dinamikusan betöltött függvények, amelyek konkrét műveletet végeznek
4. **Objektumok**: a végrehajtás célpontjai – VM-ek, szolgáltatások, konfigurációk stb.

## A működés alapelvei

* **Stateless végrehajtás** – a relay nem tárol semmit
* **Plugin-alapú kivitelezés** – minden műveletet plugin végez
* **Séma-alapú deklaráció** – minden elvárás és cél előre leírt YAML séma alapján történik
* **Függőségi gráf** – meghatározza, mit lehet mikor futtatni
* **Állapotkiértékelés** – a rendszer pillanatkép alapján dönt, nem múltbeli változások alapján

## Miért fontos a CIC?

* ✅ Teljes átláthatóság: minden művelet, feltétel és eredmény deklarált
* ✅ Újraépíthetőség: egy YAML-ból újraalkotható teljes infrastruktúra
* ✅ Automatizálhatóság: AI vagy CI/CD rendszerek általi vezérelhetőség
* ✅ Paralelizálhatóság: async relay-futtatás több szálon

## Mire nem való a CIC?

* ❌ Adattárolásra vagy konzisztens replikációra
* ❌ Rendszerek tanítására vagy automatizált döntéshozatalra
* ❌ Imperatív szkriptek közvetlen végrehajtására

## A dokumentáció szerkezete

Ez a fájl a `docs/hu/concept/` könyvtár belépési pontja. További témák:

* [`relay_pozicionalas.md`](./relay_pozicionalas.md): a végrehajtómotor működése
* [`schema_kezeles.md`](./schema_kezeles.md): hogyan épül fel egy séma
* [`plugin_interface.md`](./plugin_interface.md): mit vár el egy plugin-től a rendszer
* [`dependency_modell.md`](./dependency_modell.md): hogyan kezeljük a függőségeket
* [`graf_vegrehajtas.md`](./graf_vegrehajtas.md): a műveleti gráf feldolgozása
* [`allapotmodell.md`](./allapotmodell.md): hogyan definiáljuk és értékeljük az állapotokat
