# CIC\_Relay\_Specification.md

## CIC Relay modul – specifikáció

Ez a dokumentum a CIC Relay komponens specifikációját tartalmazza. A komponens célja, hogy validált, szabályozott adatátvitelt biztosítson a CIC rendszer különböző moduljai és végrehajtási pontjai között.

### Modul célja

A Relay a moduláris rendszer vezérlési magját képezi, amely:

* fogadja a bemenő kéréseket,
* azokhoz sémát rendel,
* továbbítja a végrehajtási gráfnak megfelelően a megfelelő végpont(ok) felé,
* majd a válaszokat érvényesíti, és visszajuttatja a hívónak.

### Fő komponensek

* **EntryPointHandler:** bemenő kérések fogadása és előfeldolgozása
* **SchemaValidator:** bemeneti és kimeneti adat sémák szerinti ellenőrzése
* **GraphRouter:** végrehajtási gráf alapján történő útvonalmeghatározás
* **RelayConnector:** kommunikációs illesztő a célvégpont(ok) felé
* **ResponseAggregator:** válaszok visszagyűjtése és egységesítése

### Fő adatstruktúrák

* **RelayRequest:** a Relay által feldolgozott kérelem objektum
* **RelayContext:** kérelem kontextus és trace azonosító
* **RelayResult:** végrehajtott válasz, állapot és metaadatok

### Funkcionális folyamat

1. Bemeneti kérés érkezik (pl. HTTP, MQ, CLI)
2. Az EntryPointHandler azonosítja és ellenőrzi az alapstruktúrát
3. A SchemaValidator validálja a bemenetet a Cabinet által regisztrált sémák szerint
4. A GraphRouter meghatározza az útvonalat a végrehajtási gráf alapján
5. A RelayConnector továbbítja a kérést a cél végpont(ok) felé
6. A ResponseAggregator összegyűjti a válaszokat és validálja azokat
7. A válasz visszakerül az eredeti hívóhoz

### Követelmények

* Minden bemenet/kimenet sémával validált
* Hibakezelés és logolás minden lépésnél
* Tranzakciós kontextus és trace ID végigkíséri a feldolgozást
* Szigorú interfész-megfelelés a Cabinet által meghatározott struktúrákkal

### Lehetséges bővítések (a PoC után)

* Több protokoll támogatás (gRPC, WebSocket)
* Dinamikus relay konfiguráció reloading
* Integrált metrika- és hibastátusz-kimenet (Prometheus / Alertmanager)

---

Ez a specifikáció a CIC Relay minimális működését írja le a PoC-környezethez. A végleges rendszer részletesebb kiterjesztéseket, jogosultságkezelést és fallback mechanizmusokat is tartalmazni fog.
