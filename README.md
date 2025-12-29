# CIC Relay (Powered by Nexus)

Ez a repository a Central Infra Core (CIC) infrastruktúra-kezelő rendszerének központi komponensét, a **Relay**-t tartalmazza. A Relay motorja a **Nexus**, egy modern, GitOps-alapú vezérlősík, amely az infrastruktúra teljes életciklusát kezeli.

## Architektúra: A Nexus Mag

A Relay nem csupán egy egyszerű proxy vagy szolgáltatás, hanem egy komplex vezérlő, amelynek központi eleme a `core/nexus` modul. A Nexus felel a rendszer "agyáért", és a következő fő komponensekből áll:

*   **`iac` (Infrastructure as Code):** Betölti és értelmezi az infrastruktúra leíróit (Desired State), majd felépíti a rendszer belső gráf-modelljét.
*   **`recorder` (State Recorder):** Minden tranzakció után egy helyi, Git-alapú cache-be rögzíti a rendszer tényleges állapotát (Actual State). Ez a "fekete doboz" biztosítja az auditálhatóságot és a hibakeresést.
*   **`crypto` (Security & Signing):** A Vault Transit engine-re támaszkodva kriptográfiailag aláír minden állapotrögzítést, garantálva azok hitelességét és sértetlenségét.
*   **`sync` (Audit Synchronization):** A helyi, részletes cache-t periodikusan tömöríti és szinkronizálja egy központi, hosszú távú audit repository-ba.
*   **`isolation` (Process Isolation):** A "Zero Trust" elvet követve, egy 3-lépcsős (Host/Relay/Module) láncon keresztül, izolált processzekben kezeli az érzékeny adatok visszafejtését.
*   **`operator` (GitOps Lifecycle):** Folyamatosan figyeli a központi infrastruktúra repository-t, és automatikusan kezeli a komponensek (pl. új hostok) életciklusát, a biztonságos "onboarding"-tól a jogosultságok visszavonásáig.

A rendszer másik fontos eleme a `core/cabinet`, amely a WASM-alapú üzleti logika futtatásáért felelős "végrehajtó" rétegként működik, a Nexus vezérlése alatt.

## Használat

### Fejlesztés és Tesztelés
A projekt `Makefile` alapú build rendszert használ.

```bash
# Tesztek futtatása
make test

# Golden tesztek (kanonikus formátum) ellenőrzése
make verify
```

### Futtatás (Prototípus)
A rendszer jelenleg prototípus fázisban van. A `cmd/nexus/main.go` egy példa implementáció, ami bemutatja a Nexus komponensek összekapcsolását. Futtatásához szükség van egy működő Vault szerverre.

```bash
export VAULT_ADDR="http://127.0.0.1:8200"
export VAULT_TOKEN="root"
go run cmd/nexus/main.go
```

## Dokumentáció
Részletes dokumentáció a `docs/` könyvtárban található.
*   [Roadmap](ai/ROADMAP.md)
*   [Mérföldkövek](docs/hu/roadmap/relay_milestones.md)
*   [Biztonsági Modell](docs/hu/security/secret_and_audit_model.md)
