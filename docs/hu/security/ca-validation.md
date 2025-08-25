# RootCA validációs útmutató

Ez a dokumentum bemutatja a Relay projekt RootCA validációs folyamatát. Leírja, hogyan tárolódik a CA publikus kulcsának ujjlenyomata, hogyan kerülnek ellenőrzésre a tanúsítványok (CRT), és hogyan használjuk a CRL-t a tanúsítvány érvényességének ellenőrzésére.

## 1. CA ujjlenyomat tárolása
A CA publikus kulcsának SHA-256 ujjlenyomatát a build metaadat tartalmazza:
```yaml
ca_fingerprint: <ca_fingerprint>
```
Ez biztosítja, hogy ellenőrzéskor pontosan azonosítani tudjuk a hitelesítő hatóságot.

## 2. Tanúsítvány (CRT) ellenőrzése
- A CRT-t a RootCA privát kulcsával írjuk alá.
- Ellenőrzéshez a CA publikus kulcsa kerül felhasználásra.
- A CRT tartalmazza a sorozatszámot és az érvényességi időszakot.

## 3. CRL ellenőrzés
- A RootCA időszakosan kiadja a Certificate Revocation List-et (CRL).
- Az ellenőrzés során a CRT sorozatszáma összevetésre kerül a CRL listával.
- Ha a CRT a listán szerepel, érvénytelen.

## 4. OCSP ellenőrzés (opcionális)
- Az Online Certificate Status Protocol (OCSP) használható valós idejű státusz lekérdezésre.
- OCSP visszaadja, hogy a CRT "good", "revoked" vagy "unknown" állapotú.

## 5. Példa CA validációs metaadat
```yaml
ca_validation:
  ca_fingerprint: <ca_fingerprint>
  crt_status: valid
  crt_serial: <crt_serial_number>
  crl_check: passed
  crl_last_update: <timestamp>
  ocsp_status: good
