# CIC – Secret & QA Evidence kezelés
## Specifikációs vázlat (draft)

### 1. Cél és hatókör
A rendszer célja:
*   Állapot-alapú QA evidenciák rögzítése Gitben.
*   Érzékeny adatok védelme Vault segítségével.
*   Reprodukálható és hiteles (aláírt) audit nyomvonal biztosítása.

Nem cél:
*   Nyers logok vagy plaintext érzékeny adatok Gitben történő tárolása.

### 2. Alapelvek
*   **Git ≠ secret store**
*   **Vault ≠ log store**
*   **CIC ≠ adat birtokos**
*   **Defense-in-depth:** kriptó + policy + process szeparáció.
*   **Plaintext:** csak minimális ideig, csak ott, ahol szükséges.

### 3. Logolási szintek (normatív)

#### 3.1 none
*   Semmi nem kerül tárolásra vagy szinkronizálásra.

#### 3.2 alap (default)
*   Gitbe kerül:
    *   expected / actual állapot snapshot,
    *   diff summary,
    *   meta (run-id, verziók, timestamp).
*   Nincs nyers I/O.
*   Daily branch push engedélyezett.

#### 3.3 magas
*   Gitbe kerül:
    *   alap szint tartalma,
    *   I/O hash/fingerprint/aggregát (nem payload).
*   Titkosított adat nem kötelező, plaintext tilos.
*   Daily branch push engedélyezett.

#### 3.4 durva (break-glass)
*   Nem része a normál rendszernek.
*   Csak lokális vagy külön, zárt remote.
*   Gitbe kerülhet:
    *   titkosított payload (Vault-managed).
*   Plaintext Gitben tilos.
*   Rövid retention.

### 4. Git struktúra és policy

#### 4.1 Branchek
*   `cache/date/*` – lokális, zajos, nem audit.
*   `daily` – audit/QA branch, squasholt napi állapot.

#### 4.2 Szabályok
*   `daily`:
    *   védett branch,
    *   no force-push,
    *   csak aláírt commit fogadható el.
*   `cache/*`:
    *   nem kötelező push,
    *   ha push: csak debug remote.

### 5. Commit hitelesítés
*   Minden audit releváns commit: kriptográfiailag aláírt (Vault Transit).
*   Aláírás tartalmazza:
    *   commit digest / manifest hash,
    *   key version,
    *   pipeline/run-id.
*   Dev hook = best effort.
*   Release/QA = relay által generált commit (normatív).

### 6. Secret-kezelési modell (Vault)

#### 6.1 Objektumok
*   host obj
*   modul obj
*   relay obj

#### 6.2 Tokenek
*   Minden obj:
    *   saját Vault token,
    *   saját path + policy (nincs átfedés).
*   Tokenek:
    *   rövid TTL,
    *   minimális capability,
    *   egymás számára nem hozzáférhetők.

#### 6.3 Feloldás
*   Rétegzett unwrap:
    ```
    relay.decrypt(
      modul.decrypt(
        host.decrypt(data)
      )
    )
    ```
*   Plaintext csak akkor keletkezik, ha üzletileg indokolt.

### 7. Runtime szeparáció
*   **3 külön process:**
    *   host-decrypt
    *   modul-decrypt
    *   relay-decrypt
*   **1 koordinátor:**
    *   sorrend, timeout, retry,
    *   nem fér hozzá tokenhez vagy plaintexthez.
*   **IPC:**
    *   csak burkolt adat,
    *   méretlimit,
    *   nincs payload logolás.

### 8. Kubernetes / Docker mód

#### 8.1 Kubernetes
*   Induláskor: egyszerhasználatos bootstrap token, csak secret seedelésre.
*   Bootstrap token:
    *   szűk policy,
    *   rövid TTL,
    *   nem használható decrypt/sign műveletre.

#### 8.2 Docker / service
*   Lokális in-memory Vault instance.
*   Központi Vaultból seedelve.
*   Plaintext nem perzisztálódik.

### 9. Tiltások (hard rules)
*   Plaintext érzékeny adat Gitben → **tilos**
*   Cache branch audit remote-ba → **tilos**
*   Wildcard Vault policy → **tilos**
*   Token megosztás obj-ek között → **tilos**

### 10. Audit és megfigyelhetőség
*   **Vault oldalon:** minden decrypt/sign esemény auditált.
*   **CIC oldalon:** ki, mikor, mely run-id készített audit commitot.
*   Break-glass események külön naplózva.

### 11. Threat-model alapállítás
*   Egy komponens kompromittálása ≠ teljes rendszer kompromittálása.
*   Plaintext megszerzéséhez több token, több policy és több process együttműködése szükséges.

---
*A rendszer célja nem a hibák elrejtése, hanem a bizonyítható, minimális kitettség melletti minőségbiztosítás.*
