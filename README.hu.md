# CIC Relay

A CIC Relay egy könnyű, plug-in-alapú keretrendszer, amely decentralizált kommunikáció, konfiguráció és állapotkezelés megvalósítására szolgál. Szorosan integrálódik a Séma Registry-vel, így egységes, sémaalapú megközelítést kínál a szolgáltatásspecifikus logika kezelésére.

---

## Fő jellemzők

* **Plug-in alapú architektúra:**
  Minden Relay példány egy vagy több szolgáltatásspecifikus plugint tölt be (például Proxmox, Ceph vagy Open vSwitch), hogy ezekhez kapcsolódó feladatokat és lekérdezéseket lásson el.

* **Sémaalapú kommunikáció:**
  Közvetlen API-hívások helyett a Relay a Séma Registry-n keresztül publikálja és kéri le a sémákat és adatokat, biztosítva az egységes, verziózott kommunikációt.

* **Delta-alapú frissítések:**
  A Relay az állapotváltozásokat („deltákat”) egy üzenetközvetítőre küldi, így minimalizálja a sávszélesség használatát, és csak a releváns állapotváltozások kerülnek továbbításra.

* **Beépített biztonság:**
  Minden művelet alapértelmezetten nem root jogosultsággal fut. Az emelt szintű jogosultságot igénylő parancsokat szigorú kontroll mellett, dedikált wrapperen vagy szolgáltatás-szintű szétválasztással kezeli.

* **Moduláris és bővíthető:**
  Új szolgáltatások további pluginek hozzáadásával integrálhatók. Minden plug-in saját sémával, resolverekkel és logikával érkezik, így a rendszer könnyedén adaptálható új környezetekhez.

---

## Projektstruktúra

```plaintext
cic-relay/
├── main.py                  # Indítóállomány
├── config.yaml              # Globális konfiguráció
├── graphql/
│   ├── schema_router.py     # Pluginek GraphQL sémáinak összefésülése
│   └── resolvers/           # Opcionális közös resolverek
├── mqtt/
│   ├── client.py            # MQTT publish/subscribe logika
│   ├── topic_map.yaml       # Topic → handler hozzárendelés
│   └── message_handler.py   # Topic handler dispatcher
├── plugins/
│   ├── base.py              # PluginBase osztály és interfész
│   ├── proxmox/
│   │   ├── plugin.py        # Konkrét plugin megvalósítás
│   │   ├── schema.graphql   # GraphQL SDL séma
│   │   └── schema.crc       # CRC/aláírás a sémához
├── utils/
│   ├── crc.py               # CRC/hash generálás és ellenőrzés
│   ├── diff.py              # Alapértelmezett vs. aktuális adat összehasonlítás
│   └── schema_loader.py     # Plugin sémák betöltése
├── tests/                   # Unit tesztek (pytest + coverage)
│   ├── test_plugin_loader.py
│   ├── test_mqtt_pubsub.py
│   └── ...
```

---

**Fontos megjegyzés:**
Ez a leírás főleg a Python-alapú implementációra készült,
a jelenlegi Go-alapú, template–instance modell, audit- és registry-centrikus architektúra részletei nincsenek teljesen lefedve benne!
