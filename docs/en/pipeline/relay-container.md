# Running the Relay as a CI Executor

This document describes how to build, start and drive the relay in a container,
so that "here is a repository and a commit" produces a signed artifact.

Before this existed, starting a relay meant a hand-assembled `docker run` with
several non-obvious flags. Those are now baked into `Dockerfile.relay`, the
`relay` service in `docker-compose.yml`, and the `relay.*` make targets.

## Quick start

```bash
make relay.build          # build the image
make relay.up             # start it, wait for /readyz
make relay.status         # health + whether ProofTrace recording is on

make relay.ci \
  REPO_URL=https://github.com/org/repo \
  COMMIT=b0d1287abbb67a3ef79bfff27a5cc6b6a98ad5ee \
  SCHEMA=postgresql VERSION=v0.18.0
```

`relay.ci` prints `build_hash`, `verification_root`, `source_ref`,
`source_tree_digest`, `cic_sign`, `chain_hash` and `source_digest`.

Read `verification_root`, not `build_hash`, when you want to know *which
revision* was built — `build_hash` is `sha256(raw_artifact)`, a pure content
digest. See [source-provenance.md](source-provenance.md).

## Configuration

The `relay` service reads these from the environment (a `.env` file next to
`docker-compose.yml` works):

| Variable | Default | Purpose |
|---|---|---|
| `CIC_RELAY_PORT` | `8080` | Published port |
| `CIC_BUILD_DIR` | `./build` | Clone/build directory — **see below** |
| `CIC_AUDIT_DIR` | `./relay-audit` | ProofTrace audit git repository |
| `CIC_SECRETS_DIR` | `./relay-secrets` | Mounted read-only at `/run/secrets/relay` |
| `CIC_BUILDER_CONTAINER` | `cic-py-builder` | Container the pipeline runs `make` in |
| `VAULT_ADDR` | — | e.g. `https://host.docker.internal:18200` |
| `VAULT_TOKEN` | — | **Without it there is no signed ProofTrace** |
| `VAULT_CACERT` | — | e.g. `/run/secrets/relay/vault-ca.crt` |
| `CIC_VAULT_MOUNT` | `transit` | Vault transit mount |
| `CIC_VAULT_KEY` | — | Signing key; the token must be *authorised for this key* |
| `CIC_TRUST_MODE` | `dev` | PKI trust mode |

### `CIC_BUILD_DIR` must be shared with the builder container

The relay clones the repository into this directory and then runs
`docker exec <builder_container> make <target>`. The builder container is a
separate container: if it does not mount the **same host path**, it cannot see
the clone, and `make` runs against an empty or stale directory.

The builder's working directory must also be the clone path, because the relay
invokes `docker exec` without `-w`.

### Vault

Two failure modes look similar but are not:

- **No `VAULT_TOKEN`** — the relay starts normally and logs
  `ProofTrace recording disabled`. Everything works except that no signed trace
  is produced. `make relay.status` reports this explicitly.
- **Token present but not authorised for `CIC_VAULT_KEY`** — the run fails at
  the signing step with a Vault `403 permission denied`. A token can be
  perfectly valid and still lack permission on that particular key.

Vault normally runs on the host. The container is on a bridge network, so
`127.0.0.1` does not reach it; the service declares
`host.docker.internal:host-gateway` for this, so use
`VAULT_ADDR=https://host.docker.internal:<port>`.

## What the image bakes in

These were the pitfalls of the hand-assembled setup, and each is now handled in
the image rather than by the operator:

| Symptom | Cause | Handled by |
|---|---|---|
| `error obtaining VCS status: exit status 128` | Go's VCS stamping fails when the build context has no usable `.git` (CI checkouts, read-only mounts) | `-buildvcs=false` in the build stage |
| `git clone … detected dubious ownership` | The clone target is owned by a different uid than the process | `/etc/gitconfig` with `safe.directory = *`, `GIT_CONFIG_GLOBAL` set. The env-var form (`GIT_CONFIG_COUNT`/`KEY_0`/`VALUE_0`) does **not** work here |
| `docker exec: executable file not found in $PATH` | The pipeline shells out to `docker`, which a Go base image does not have | docker **client** copied from `docker:28-cli`; the daemon is the host's via the mounted socket |
| `mkdir /var/lib/cic-relay: permission denied` | The audit recorder needs a writable path | Directory created in the image, exposed as a volume |
| `cic.pipeline.start: build_dir is required` | No default build directory | `CIC_SCHEMA_BUILD_DIR=/build` |
| `cic.pipeline.test: builder_container is required` | No default builder container | `CIC_BUILDER_CONTAINER` in the compose service |

## Notes

- The service is behind the `relay` compose profile, so it does not start with
  `docker compose up -d` and the existing build/tooling stack is unaffected.
- The image ships no `curl`/`wget` and declares no `HEALTHCHECK`; poll
  `/healthz` and `/readyz` from outside, as `make relay.up` does.
- `CommitHash` / `SourceTreeHash` build args describe the **relay's own** source.
  They only reach a ProofTrace when a run carries no repository of its own —
  see [source-provenance.md](source-provenance.md).
