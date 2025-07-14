# CIC Relay – Trusted Supply Chain

Ez a dokumentum a CIC Relay architektúra Trusted Supply Chain bizalom- és audit-funkcióinak részletes konceptuális alapjait írja le, előkészítve a teljes technikai specifikációt és integrációs leírást.

---

## 1. Célrendszer és alkalmazási környezet

A CIC Relay a teljes infrastruktúra supply chain audit és digitális bizalom központi eleme. Multi-tenant, geo-redundáns, felhő (private/public) és hibrid környezetekben, valamint elosztott vállalati és kritikus rendszerekben is működhet. Központi szerepe, hogy a rajta áthaladó vagy ott generált minden komponens (modul, konfiguráció, workflow, release) eredete, állapota és audit trail-je kompromisszummentesen visszakövethető legyen.

## 2. Fő komponensek, logikai egységek

* **Relay modul:** hitelesítő, validáló, auditáló “gatekeeper”, amely minden be- és kimenő adatot, workflow-t vagy modult digitális aláírással és audit loggal lát el.
* **Workflow engine:** moduláris végrehajtási logika, ahol minden lépés (indítás, módosítás, futtatás) auditált.
* **Config store:** minden konfigurációs vagy állapotobjektum digitálisan nyomozható (hash, signature, version).

## 3. Részletes működési logika

* **Digitális aláírás generálás:** minden pipeline vagy release folyamat végén automatikusan generálódik. Kulcskezelés: belső CA vagy integrált külső CA-chain is támogatott, policy-k alapján.
* **Ellenőrzési pontok:** on-load, on-exec, on-release, update, rollback, valamint manuális ellenőrzési workflow-k. Minden komponens beépített signature/issuer validációval kerül a rendszerbe.
* **Audit log séma:** kötelező mezők: timestamp, issuer, signature, objektum azonosító, workflow-id, CA-chain, változás típusa, metaadatok. Backend: immutable, append-only storage (pl. object store, blockchain kompatibilitás opció).
* **Per-modul compliance flow:** minden komponens audit trail-je bármikor visszakereshető, minden API/CLI/GUI művelethez validáció kapcsolódik.
* **Kompromisszummentes validáció:** minden kritikus workflow-nál vagy rendszerindításnál runtime signature és CA-chain ellenőrzés (nem csak build-time-on), minden útvonalon.

## 4. Integrációs minták

* **CI/CD pipeline integráció:** támogatott példák: GitHub Actions, GitLab CI, Jenkins, ArgoCD, FluxCD stb.
* **Release menedzsment:** automatikus vagy manuális kiadásmenedzsment, signature-verification és release approval lépések.
* **CA-integráció:** lehet dedikált belső CA, vagy enterprise/public CA, CRL/OCSP támogatással. Képes harmadik fél issuer-ek kezelésére is.

## 5. Védelmi és szabályozási megfelelés

* **Compliance mapping:** minden főbb előírás (DORA, NIS2, FDA GxP, SOX, CMMC, ISO 27001) compliance mappingje (példákkal, hivatkozással).
* **Kötelező trusted build chain audit:** minden szabályozott release-nél kötelező audit workflow, igazolható digitális aláírás és immutable log.
* **Incidens visszakeresési workflow:** audit hibánál, signature mismatch-nél, CA-revoke-nál pontos trace és ok-okozati lánc visszavezetés.

## 6. Példák és best practice

* **YAML/JSON release manifest minta:** signature és audit mezőkkel.
* **Audit log entry példa:** valós metaadatokkal és signature-ral.
* **Hibaesetek:** signature hibák, CA feketelistázás, release visszavonás – mit jelent és hogyan kezelhető.

## 7. Határok, nem célok

* **Nem cél:** harmadik féltől származó, nem auditált modulok automatikus trusted kezelése, teljes runtime sandboxing, off-chain fejlesztési folyamatok kezelése.
* **Fejlődési roadmap:** minimum viable compliance baseline definiálása, CICtor-ready architektúra támogatása, folyamatosan bővíthető compliance funkciók.

---

**Ez a dokumentum a CIC Relay Trusted Supply Chain funkcióinak specifikációs alapja. Következő lépés: konkrét séma, workflow és API-definíció kidolgozása, valamint első PoC implementációk elindítása.**
