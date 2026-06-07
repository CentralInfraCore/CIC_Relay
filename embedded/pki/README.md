# DEVELOPMENT ONLY — Embedded PKI Assets

**WARNING: The `.key` files in this directory are generated test/development keys.**
They are NOT used in production and must NEVER be used in any real environment.

These assets exist solely to support the embedded component loading tests.
Production PKI material is injected at runtime from Vault or a separate secure store.

## Contents

| File | Purpose |
|---|---|
| `root_ca.key` | Dev/test root CA private key |
| `root_ca.pem` | Dev/test root CA certificate |
| `intermediate_ca.key` | Dev/test intermediate CA private key |
| `intermediate_ca.pem` | Dev/test intermediate CA certificate |

## Regenerating

These keys are generated at test setup. See `cmd/relay/testdata/bootstrap_testdata.sh`
for the reference generation script.