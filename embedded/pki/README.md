# DEVELOPMENT ONLY — Embedded PKI Assets

**WARNING: The `.key` files in this directory are generated test/development keys.**
They are NOT used in production and must NEVER be used in any real environment.

These assets exist solely to support the embedded component loading tests.
Production PKI material is injected at runtime from Vault or a separate secure store.

**Never embed the certificate of a key that is kept here.** The Relay's dev trust store
(`CIC_TRUST_MODE=dev`, which is also the default) trusts every self-signed CA under
`cmd/relay/embedded/pki/`. A CA whose private key is in this repository would let anyone who
can read the repository issue a chain that the dev trust store accepts. A test in `cmd/relay`
(`TestEmbeddedTrustHasNoRepoKnownPrivateKey`) fails if that happens.

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