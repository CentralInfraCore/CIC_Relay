# Commit aláírás (Commit Signing) útmutató

Ez a dokumentum a fejlesztői commit aláírás beállítását és ellenőrzését mutatja be a Relay projektben.

## 1. GPG kulcs létrehozása és konfiguráció
```bash
gpg --full-generate-key
```
Típus: RSA vagy Ed25519

Listázás:
```bash
gpg --list-secret-keys --keyid-format=long
```
Állítsd be a Git konfigurációban:
```bash
git config --global user.signingkey <kulcs_ID>
git config --global commit.gpgsign true
```

## 2. GPG Agent cache beállítás
`~/.gnupg/gpg-agent.conf` fájl:
```conf
default-cache-ttl 86400
max-cache-ttl 604800
```
Agent újraindítása:
```bash
gpgconf --kill gpg-agent
```
Ez biztosítja, hogy naponta csak egyszer kell jelszót/pint megadni.

## 3. Commit aláírás használata
- Automatikusan: minden commit aláírt (`commit.gpgsign=true`).
- Kézzel: egyedi commit aláírás
```bash
git commit -S -m "Aláírt commit"
```

## 4. Pipeline ellenőrzés
Pipeline build indulásakor ellenőrzi:
```bash
git verify-commit HEAD
```
Érvénytelen vagy hiányzó aláírás esetén a build megszakad.

## 5. Branch alapú automatikus aláírás (példa hook)
`prepare-commit-msg` hook:
```bash
#!/bin/sh
BRANCH=$(git rev-parse --abbrev-ref HEAD)
if echo "$BRANCH" | grep -q "^release/"; then
  exec git commit -S "$@"
fi
```
Ez biztosítja, hogy `release/*` branch commitjai mindig aláírtak legyenek.

## 6. Ellenőrzés fejlesztői oldalon
Commit aláírás ellenőrzése:
```bash
git log --show-signature -1
```
