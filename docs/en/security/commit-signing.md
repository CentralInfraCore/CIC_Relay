# Commit Signing Guide

This document describes the developer commit signing setup and verification process for the Relay project.

## 1. Generating and configuring a GPG key
```bash
gpg --full-generate-key
```
Type: RSA or Ed25519

List keys:
```bash
gpg --list-secret-keys --keyid-format=long
```
Set up Git to use the key:
```bash
git config --global user.signingkey <key_ID>
git config --global commit.gpgsign true
```

## 2. GPG Agent cache configuration
In `~/.gnupg/gpg-agent.conf`:
```conf
default-cache-ttl 86400
max-cache-ttl 604800
```
Restart the agent:
```bash
gpgconf --kill gpg-agent
```
This ensures the passphrase/PIN is required only once per day.

## 3. Using commit signing
- Automatically: all commits are signed (`commit.gpgsign=true`).
- Manually: sign individual commits
```bash
git commit -S -m "Signed commit"
```

## 4. Pipeline verification
When the build pipeline starts, it verifies:
```bash
git verify-commit HEAD
```
If the signature is invalid or missing, the build fails.

## 5. Branch-based automatic signing (hook example)
`prepare-commit-msg` hook:
```bash
#!/bin/sh
BRANCH=$(git rev-parse --abbrev-ref HEAD)
if echo "$BRANCH" | grep -q "^release/"; then
  exec git commit -S "$@"
fi
```
This ensures that `release/*` branch commits are always signed.

## 6. Developer-side verification
Check commit signature:
```bash
git log --show-signature -1
```
