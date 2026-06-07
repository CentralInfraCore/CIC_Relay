# Cabinet\_Specification.md

## Cabinet modul – funkcionalitási és architekturális specifikáció

Ez a dokumentum a Cabinet modul magas szintű működését és architekturális felépítését írja le. A cél a modul szerepének, komponenseinek és interfészeinek pontos meghatározása.

### Modul célja

A Cabinet modul egy központi regiszter és érvényesítő komponens, amely a rendszer működéséhez szükséges:

* sémákat,
* pluginokat,
* végrehajtási gráfokat

nyilvántartja, validálja és visszakereshetővé teszi.

### Fő funkciók

1. **Sémakezelés:**

    * `RegisterSchema(SchemaDef)` – új séma regisztrálása
    * `GetSchema(name)` – séma lekérdezése név alapján
    * `ListSchemas()` – elérhető sémák listázása

2. **Pluginkezelés:**

    * `RegisterPlugin(PluginDescriptor)` – új plugin regisztráció
    * `GetPlugin(name)` / `GetValidator(name)` – plugin visszakeresés név szerint
    * `ListPlugins()` – elérhető pluginok listázása

3. **Gráfkezelés:**

    * `RegisterGraph(ExecutionGraph)` – új gráf regisztrálása
    * `GetGraph(name)` – gráf lekérdezése
    * `ListGraphs()` – regisztrált gráfok listája

4. **Intent feloldás:**

    * `ResolveIntent(IntentDef)` – egy intent alapján a megfelelő végrehajtási terv generálása (ExecutionPlan)

### Fő komponensek

* **SchemaStore:** belső struktúra a sémák tárolására
* **PluginRegistry:** validátorok és pluginok kezelése
* **GraphRegistry:** a végrehajtási gráfok reprezentációja és kapcsolatai
* **IntentResolver:** az intent → végrehajtási terv fordítás logikája

### Adatstruktúrák

A belső adattárolás strukturált map-ekkel történik, a kulcs a név/verzió kombináció. A típusok Go `struct` formátumban kerülnek reprezentálásra.

### Követelmények

* Szálbiztos működés
* Hibakezelés minden interfészen
* Teljes introspektálhatóság (minden regisztrált elem lekérdezhető)

### Használati forgatókönyvek (példák)

1. Új plugin és séma regisztrálása, majd gráf létrehozása belőlük
2. Egy intent alapján automatikus végrehajtási terv generálása
3. Hibás vagy hiányos definíció regisztrációja esetén hiba visszajelzése

---

Ez a specifikáció a Cabinet modul minimálisan elvárt működését írja le a PoC környezetben. A további kiterjesztések a rendszer komplexitásának növekedésével következnek.
