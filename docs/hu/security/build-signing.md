# Build aláírási útmutató

Ez a dokumentum bemutatja a Relay projekt build aláírási folyamatát és annak RootCA validációhoz való integrációját.

## 1. Build metaadatok
A build során a következő metaadatok kerülnek be a binárisba `ldflags` segítségével:
```yaml
build_id: relay-<version>-<commit>
commit: <commit_hash>
timestamp: <build_timestamp>
gpg_signature: <build_signature>
ca_fingerprint: <ca_fingerprint>
```

## 2. RootCA aláírás
- A commit azonosítót (és opcionálisan az artefaktum hash-t) a build rendszer aláírja a RootCA privát kulcsával.
- Az ellenőrzéshez a publikus kulcs kerül felhasználásra.

## 3. Pipeline aláírási folyamat
1. Fejlesztő commit aláírásának ellenőrzése (`git verify-commit`).
2. Bináris fordítása a commit ID-val és a RootCA aláírással beágyazva.
3. Külső YAML meta fájl generálása ellenőrzéshez:
```yaml
build_meta:
  commit: <commit_hash>
  commit_signature: <gpg_signature>
  ca_fingerprint: <ca_fingerprint>
  binary_sha256: <binary_hash>
```

## 4. Ellenőrzési folyamat
- A bináris `-v` kapcsolója kiírja a beágyazott metaadatokat.
- A külső meta YAML összehasonlításra kerül a beágyazott metaadatokkal.
- A RootCA publikus kulcsával ellenőrzik a build aláírását.

## 5. Biztonsági elv
Ez biztosítja:
- A forrás commit hitelességét (fejlesztő GPG aláírás).
- A build folyamat integritását (RootCA aláírás).
- Auditálhatóságot a külső meta fájl és a bináris metaadat egyezőségével.