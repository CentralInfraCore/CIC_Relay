# Cabinet\_Api\_Test\_Specification.md

## Cabinet modul – API tesztelési specifikáció (magyar változat)

Ez a dokumentum a Cabinet modul API-felületének tesztelési specifikációját tartalmazza. A cél a komponens funkcionális, robusztussági és integrációs tesztelése.

### Tesztelési célok

* Az API által nyújtott funkciók helyes működésének ellenőrzése
* Helytelen bemenetekre adott válaszok validálása
* Szálbiztonság tesztelése
* Sémaérvényesítés és plugin-regisztráció ellenőrzése

### Tesztesetek csoportosítása

1. **Sémaregisztrációs tesztek**

    * Érvényes `SchemaDef` regisztráció
    * Duplikált sémanév kezelése
    * Hiányos vagy hibás sémaszerkezet elutasítása

2. **Pluginregisztrációs tesztek**

    * Érvényes plugin regisztráció
    * Hiányzó input/output séma tesztje
    * Duplikált plugin név

3. **Gráfregisztrációs tesztek**

    * Érvényes `ExecutionGraph` regisztráció
    * Nem létező séma hivatkozás a gráfban

4. **Intent feloldás tesztek**

    * Egyszerű végrehajtási terv létrehozása ismert intent alapján
    * Ismeretlen vagy inkompatibilis intent kezelése

5. **Thread-safety tesztek**

    * Párhuzamos séma/plugin/intents kezelés
    * Adatverseny és inkonzisztencia detektálása

6. **Listázó függvények**

    * Üres és feltöltött állapot listáinak ellenőrzése

### Tesztelési módszerek

* Unit tesztek (Go `testing` csomaggal)
* Mock implementáció használata
* Concurrency-tesztek (pl. `go test -race`)
* Hibaválaszok összehasonlítása várt típusokkal

### Elvárt kimenetek

Minden függvény:

* Érvényes bemenetre sikeres választ ad
* Hibás bemenetre specifikált hibatípussal válaszol
* Párhuzamos hívások esetén konzisztens marad

---

További tesztesetek és edge-case elemzések a PoC során kerülnek feltöltésre.
