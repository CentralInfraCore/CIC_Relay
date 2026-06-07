# Tanúsítvány Validáció a CIC rendszerben

Ez a dokumentum a CIC rendszer egyedi tanúsítványkezelési és bizalmi modelljét írja le, amely lehetővé teszi a Root CA cseréjét anélkül, hogy a meglévő objektumok hitelessége sérülne. A cél egy olyan determinisztikus és auditálható hitelesítési mechanizmus, amely minden környezetben, offline módon is végrehajtható.

## Alapelvek

* Minden aláírt objektum hordozza a hitelességéhez szükséges adatokat.
* Az objektum aláírását végző tanúsítvány (Certificate) hitelessége külön kerül ellenőrzésre.
* A rendszer képes kezelni a Root CA rotációt úgy, hogy közben a korábbi objektumok továbbra is biztonságosan érvényesíthetők maradnak.

## Kulcsmezők

* `Signature`: az objektum tartalmi hash-e (`Checksum`) aláírva az aktuális tanúsítvánnyal.
* `Certificate`: az aláíró tanúsítvány PEM formátumban.
* `CertificateSignature`: a `Certificate` mező aláírása egy megbízható (régebbi) Root CA által.

## Validációs lépések

1. **Tanúsítvány eredetének ellenőrzése**

    * A `CertificateSignature` ellenőrzése történik meg egy, a rendszer által ismert és megbízható Root CA alapján.
    * Ha érvényes, a `Certificate` elfogadható hiteles aláíróként.

2. **Objektum aláírásának ellenőrzése**

    * A hitelesített `Certificate` alapján ellenőrizhető a `Signature` a `Checksum` fölött.
    * Ha mindkét lépés sikeres, az objektum hitelesnek tekinthető.

## Miért biztonságos?

* A `Certificate` tartalmát nem fogadjuk el önmagában — az is alá van írva.
* A `CertificateSignature` révén a cert származása bizonyítható.
* A rendszer determinisztikusan és offline is képes eldönteni a bizalmat.

## Előnyök

* Root CA váltás biztonságos kezelése, visszamenőleges hitelesítéssel.
* Minden objektum önállóan hordozza a hitelesítéshez szükséges elemeket.
* Nem függ központi adatbázistól, OCSP-től vagy CRL-től (de később kiegészíthető).

## Archiv vagy hosszúlejáratú CA alkalmazása komprimitálódás esetén

A rendszer támogatja archív vagy hosszúlejáratú Root CA-k integrálását is, amelyek kompromittálódás vagy bizalmi lánc sérülése esetén biztosítják a meglévő objektumok hitelességének visszamenőleges igazolását, akár évtizedekkel a kiállítás után is. Ez a megközelítés lehetővé teszi a teljes compliance- és auditképességet kritikus környezetekben.

* Kompromittálódás esetén az aktuális Root CA visszavonható, de a meglévő objektumok továbbra is validálhatók maradnak az archív CA-val.
* A hosszúlejáratú, offline (pl. trezorban tárolt) archív CA csak vészhelyzetben vagy audit esetén kerül használatra, ezzel biztosítva a múltbeli adatok hitelességének védelmét.
* Ezzel a rendszer akár évtizedekkel később, bármilyen hálózati környezetben is képes hitelesíteni a régi adatokat.
* A bizalmi korszakok (TrustEpoch) közötti átmenet is biztonságosan és auditálhatóan kezelhető.

## Modulok viselkedése

* Minden modul a saját tanúsítványát és a hozzá tartozó bizalmi láncot betölti induláskor.
* Validáció során:

    * Először a `CertificateSignature` alapján eldönti, elfogadható-e a `Certificate`.
    * Ezután ellenőrzi az objektum `Signature` mezőjét.

## Továbblépési lehetőségek

* Verziózott „bizalmi korszakok” bevezetése (`TrustEpoch`).
* CRL vagy OCSP használata hálózati kontextusban.
* Tanúsítványlánc-tömörítés vagy bináris kódolás a méret csökkentéséhez.
