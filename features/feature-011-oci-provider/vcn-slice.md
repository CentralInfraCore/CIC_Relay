# The VCN slice: Intent -> Observed -> Conformance/Drift -> Evidence (fixture-driven)

Status: **scaffold** for the chain as a whole, **implemented** for each piece the test uses on its own. Nothing executes: no OCI
call, no signing, no Vault, no workflow. The slice is walked on files by `cmd/relay/vcn_slice_test.go` over the common fixture
`cmd/relay/testdata/vcn-v1` (vendored unchanged from cic-factory `fixtures/vcn-v1`, commit
`e5978f50d2512c5ac7536b9d81ffb1f054b75525`; the test checks it against its `MANIFEST.sha256`).

## The chain and who owns each hop

```
intent (registry names) + OCI GetVcn answer
   │ 3  module: normalize  ->  effective config, state, coverage (OCI names)     cic-module-oracle-cloud   (expected/observed.oci.json)
   │ 4  registry binding   ->  registry names + coverage per compared path       cic-schema-registry       (expected/observed.registry.json)
   │ 5  conformance        ->  per-path verdicts + object verdict (plan.json)    cic-primitive-engine      (expected/verdict.fields.json)
   ▼ 6  evidence           ->  hash-chained record + claims                      CIC-Relay (this test)     (expected/evidence.claims.json)
```

| Hop | Status | Where the bridge is |
|---|---|---|
| 3, 4, 5 each in its own repo | owned by other workers; this repo only reads their expected outputs | **the model exists, but the implementation bridge breaks here**: nothing at runtime composes registry + module + engine |
| 5 inside this repo (`core/nexus/iac` `Evaluate`) | **implemented** (an earlier, parallel Go implementation of the same semantics; it has no `unknown` coverage) | not called by anything in `cmd/relay` at runtime (`iac` is used by tests and the loader) |
| 6 the chain (`cabinet.ComputeChainHashV1`, `ProofArtifact`, `VerifyProofArtifact`) | **implemented** | used here on fixture data only |
| 6 the claim rules and the sidecar | **scaffold**: they exist only in `vcn_slice_test.go` | no production code builds this evidence |

## How the evidence is built without a new schema

`ProofTraceStep` has only `name`, `module`, `input_hash`, `output_hash`, and `ProofArtifact` has no verdict field. The test does not
add one. It makes **one step per stage** (`normalize-oci`, `bind-registry`, `conformance`, `claims`; module `vcn-slice/fixture`),
with the hash of the canonical JSON of each stage artifact as input and output, `source_digest` = SHA-256 of the fixture manifest,
`timestamp` injected. The verdict enters the chain as the **output hash of the `conformance` step** and the input hash of the
`claims` step. What a reader needs in words (the coverage per path, the verdict per path, the drift and unobserved paths, the claim)
is a **sidecar** document; the output hash of the last step is the hash of that sidecar, so it cannot be changed without the chain
noticing. `pose_result` is left unset on purpose: it describes the proof-of-state-existence check, not conformance, and filling it
with the conformance object would mix two different meanings of "drift". The sidecar is a test-side struct, not a Relay schema; making
it one is a decision for the owner.

## The claim rules (asserted by the test)

* Conformance is claimed **only** if the object verdict, **recomputed** from the field verdicts (never taken from the file), is
  `CONFORMANT`, and no compared path has coverage `unobserved` or `unknown`.
* `unknown` and `unobserved` appear in the sidecar as they are; they are never rewritten to `false` or `conformant`.
  A 404 `NotAuthorizedOrNotFound` is `unknown` (deleted and forbidden look the same, so absence is not authoritative); a timeout is
  `unobserved`; a missing `ipv6CidrBlocks` leaves `ipv6_enabled` `unobserved`, not `false`.
* The evidence builder **refuses** (error, no record) a verdict that contradicts its fields, a `CONFORMANT` field whose coverage is
  not `observed`, and a compared path without coverage.
* A changed output hash of any step, or a forged claim, is detected (`VerifyProofArtifact`, the sidecar binding).

## Differential against `iac.Evaluate`

Each case's registry-side observed state is evaluated by the Relay's own `iac.Evaluate` (coverage mapped into the two lists of
`iac.Observation`) and compared with the fixture's expected verdicts. Result on this commit:

| Case | Divergence | Kind |
|---|---|---|
| all 9 cases | none in any field verdict or object verdict | — |
| `desired-absent-observed-present` | none: `iac` gives `NOT_COMPARABLE` for the undeclared planned path, as the engine does | (open question, below) |
| `malformed-cidr` | coverage `unknown` (1 path) is folded into `unobserved` | `iac.Observation` has no name for `unknown` |
| `not-found` | coverage `unknown` (5 paths) is folded into `unobserved` | same |

So the **verdicts agree**; what the Go envelope loses is the *distinction* between `unknown` and `unobserved`, which matters for
the evidence (and for its observation digest), not for the verdict. `TestVCNSlice_GoIacDifferential` freezes this exact list: a new
or vanished difference fails the test. `iac` is not changed to make them agree.

## The open question (fixture case `desired-absent-observed-present`)

The oracle module treats a field that is absent in the desired state as *unmanaged* (do not touch). The engine, and `iac.Evaluate`
with it, compares every **planned** path, and for a planned path the intent does not declare but OCI reports gives
`NOT_COMPARABLE` (object `DRIFT`). Either the plan is derived from the intent (an undeclared path is not planned) or the engine must
tell *unmanaged* from *incomparable*. The fixture records the engine's actual behaviour; this test does not decide it.

## Not covered here

Anything that executes: signing of the evidence, the Vault-backed ProofTrace recorder, `pose_result`, a real `Observe`. Companion
`.yaml` files of the KB were not generated for the new Go file.
