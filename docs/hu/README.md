# CIC Relay

A CIC Relay egy ultra-minimalista, deklaratív, plug-in-alapú végrehajtási motor, amely decentralizált kommunikáció, konfiguráció és állapotkezelés megvalósítására szolgál.
Szorosan integrálódik a Séma Registry-vel, így egységes, sémaalapú megközelítést kínál a szolgáltatásspecifikus logika és workflow-k kezelésére.

---

## Fő elvek és jellemzők

* **Plug-in architektúra**
  Minden funkció, feldolgozási logika, validáció vagy compliance kizárólag modul (plug-in) formájában csatlakozik.
  Minden Relay példány szolgáltatásspecifikus plugineket (pl. Proxmox, Ceph, Open vSwitch) tölthet be, ezek sémával, resolverrel és saját logikával dolgoznak.

* **Deklaratív template–instance modell**
  A rendszerben minden workflow, modul és séma csak sablonként (template-ként) van jelen a registry-ben.
  Futás közben csak workflow instance példányosodik, amely referenciaként használja a sablonokat.
  A pipeline, modul és séma sosem duplikálódik – ez biztosítja a minimális memóriahasználatot és a gyors skálázást.

* **Sémaalapú kommunikáció és validáció**
  Minden adat- és állapotkezelés explicit sémán alapul.
  A Relay a Séma Registry-n keresztül publikálja és kéri le a sémákat, adatokat, így biztosítva az egységes, verziózott, auditálható kommunikációt.

* **Delta-alapú frissítések**
  Az állapotváltozások („delták”) üzenetközvetítőre kerülnek, csak a releváns változások továbbítása történik – ez minimalizálja a hálózati terhelést.

* **Audit és compliance**
  Minden workflow instance teljes folyamat- és állapotnaplót vezet.
  A rendszer determinisztikus, minden lépése visszakövethető – így könnyen auditálható, compliance-ready, akár enterprise környezetben is.

* **Objektum-registry és gyors kereshetőség**
  Minden sablon és példány egyedi azonosítóval, O(1) elérésű, indexelt registry-ben található.
  Ez biztosítja a gyors lookupot, nagy párhuzamos terhelés mellett is.

* **Beépített biztonság**
  Alapértelmezetten minden művelet nem root jogosultsággal fut.
  Az emelt szintű jogosultságot igénylő parancsokat dedikált wrapperrel vagy szolgáltatás-szintű szétválasztással, szigorúan kontrolláltan kezeli.

* **Moduláris és bővíthető**
  Új szolgáltatások, logikai modulok, workflow-k vagy sémák csak új template-ek és pluginek regisztrálásával hozzáadhatók.
  A rendszer minden új funkciót azonnal integrál, a core újrafordítása nélkül.

---

## Pluggable kommunikációs és integrációs modulok

A CIC Relay architektúrája teljes mértékben támogatja, hogy
bármilyen kommunikációs vagy orchestration technológia – legyen az **GraphQL**, **message queue** (NATS, MQTT, RabbitMQ, Kafka),
vagy akár REST API – **modulként vagy külső szolgáltatásként csatlakozzon a pipeline-ba.**

* **GraphQL**:

    * Beépíthető akár pipeline-modul/lépésként, akár külön “API service”/adapterként.
    * Lehetővé teszi a schema-driven adatfeldolgozást, introspection-t, automatikus dokumentációt.
* **Message Queue (messagesQ)**:

    * Async event-dispatch vagy orchestration is támogatható, akár plug-in, akár külső orchestrator szerepben.
    * Az egész rendszer audit- és compliance-fókuszú, így a message queue integráció minden tranzakciót naplózni tud.

**A rendszer minden ilyen integrációt explicit, deklaratív, pipeline-alapú plug-in interfésszel kezel,
nincs “beleégetett” technológiai kötöttség. Ez biztosítja a hosszú távú rugalmasságot és bővíthetőséget.**

---

## Projektstruktúra

```plaintext
cic-relay/
├── cmd/                    # Főindító binárisok
├── core/
│   ├── registry/           # Objektum registry implementáció
│   ├── pipeline/           # Pipeline végrehajtás
│   ├── plugins/            # Beépíthető modulok (Go interface-ek)
├── api/
│   ├── graphql/            # GraphQL (vagy REST) adapter(ek)
│   ├── mq/                 # Message queue interfészek
├── pkg/
│   ├── schema/             # Séma definíciók, validátorok
│   └── util/               # Segédfüggvények
├── config/                 # Konfigurációs példák, template-ek
├── docs/                   # Dokumentáció, ábrák, pipeline minták
├── tests/                  # Tesztek, pipeline tesztpéldák
```

> *A fenti struktúra illeszkedik a deklaratív, plug-in-alapú, template–instance architektúrához,
> de a rendszer rugalmasságából adódóan a mappák testre szabhatók, bővíthetők.*

---

## Fogalomjegyzék (rendszer-szintű kulcsfogalmak)

| Típus    | Sablon (template)                | Példány (instance/object)                |
|----------|----------------------------------|------------------------------------------|
| Workflow | workflow\_template               | workflow\_instance / workflow\_obj       |
| Modul    | modul\_template                  | modul\_obj (workflow instance-ben)       |
| Séma     | schema\_template                 | (csak validáció eredmény, nincs példány) |
| Registry | Központi tár, minden template/id | Instance registry csak az állapothoz     |

---

## Hogyan működik?

1. **Induláskor**: minden workflow, modul, séma template betöltődik a registry-be (pl. YAML/JSON-ből vagy adatbázisból).
2. **Workflow trigger**: új esemény vagy bemenet hatására egyedi workflow instance jön létre, saját állapottal, egyedi azonosítóval.
3. **Pipeline futás**: az instance végighalad a deklaratív pipeline-on, minden lépésben csak hivatkozik a megfelelő modul- és schema template-re.
4. **Állapot- és logkezelés**: az összes feldolgozási eredmény, log és audit napló kizárólag az instance-hoz tartozik.
5. **Bővíthetőség**: új modul, workflow vagy séma hozzáadásához csak template-regisztráció szükséges.

---

## Példa pipeline-leíró (YAML)

```yaml
pipeline:
  steps:
    - id: validate_user
      module: validate_user
      input_schema: user_schema
      output_schema: validated_user_schema
    - id: compliance
      module: gdpr_compliance
      input_schema: validated_user_schema
      output_schema: compliant_user_schema
```

---

## Architektúra-vázlat (ascii)

```
[workflow_template]--+
                     |
                +----v----+
                | workflow_instance |
                +----+----+
                     |
  +------------------+-------------------+
  |                  |                   |
[modul_template] [modul_template] ... [schema_template]
  |                  |                   |
(instance állapot/log csak az instance-ban változik)
```

---

## Megjegyzés

Ez a leírás már a jelenlegi Go-alapú, template–instance architektúrát, plug-in pipeline modellt és registry-centrikus működést tükrözi.
Minden régi Python-alapú megközelítéshez képest a hangsúly a skálázhatóságon, auditon, plug-in modularitáson és a deklaratív workflow szemléleten van.

---

## Jogok

Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International (CC BY-NC-SA 4.0)
