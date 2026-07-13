# `http.request` capability — Setx-driven actuation (feature-009)

Status: **implemented** (`core/modules/httpcap`, cabinet integration). The
relay-integration span of the audit bridge: the HTTP actuation is now a **native
cabinet capability** that `Setx` drives as an ordinary workflow step — so a
capability call folds into the same hash-chained `ProofTrace` (and, via the
recorder, the same Vault-signed Git commit) as any native or WASM step, with no
special-casing.

## Shape

- `core/modules/httpcap.Actuator` — the seam. One method,
  `RunFlow(reqJSON []byte) (resJSON []byte, error)`, matching the Rust FFI
  `run_flow` document byte-for-byte. The production implementation is the cgo FFI
  (`cicffi.RunFlow`), injected at relay startup; `httpcap` and the cabinet never
  import cgo, so the default build stays pure-Go. `ActuatorFunc` adapts a plain
  function (or a test double).
- `NewRequestFunc(actuator)` — returns the native module function
  (`func(context.Context, interface{}) (interface{}, error)`). It marshals the
  step input as the `run_flow` request, actuates, and returns a non-secret
  `Result` (status, base64 body, countersigned scope, and the `Audit` record with
  its tamper-evident `record_hash`). A refused actuation (policy deny, SSRF block,
  limit, transport error) is surfaced as a step error; the credential and body
  contents never cross into the result. A nil actuator yields `ErrNoActuator`.

## Why this is the whole point

Setx already fingerprints every native step (input hash → dispatch → output
hash → `ProofTraceStep`) and chains it into the `ProofTrace`. So registering the
actuation as a native module means:

```
workflow step  http@1.0.request(input)
  → Setx hashes input → httpcap.NewRequestFunc → Actuator (FFI run_flow)
    → Setx hashes output → ProofTraceStep → ChainHash
      → ExecutionRecorder → Vault-signed Git commit   (existing machinery)
```

No bespoke audit path: a capability actuation is auditable and durable exactly
like any other step.

## Tested

- `httpcap` unit tests: success (non-secret `Result`, secret never in output),
  refused actuation → error, nil actuator → `ErrNoActuator`, actuator error /
  garbage result / unmarshalable input → error. Coverage 100%, gremlins efficacy
  100% (added to the CI mutation list).
- Cabinet integration (`core/cabinet/httpcap_integration_test.go`): a real
  `Setx` runs a `http@1.0.request(input)` workflow through a mock `Actuator`,
  asserting the credential reaches the actuator (Relay→Rust), exactly one chained
  `ProofTrace` step is produced (`module = http@1.0`), the `ChainHash` is
  reproducible, and the credential never reaches the trace.

## Still open (the deploy span)

- **Bootstrap registration + cgo actuator**: registering `http@1.0` in
  `cmd/relay/bootstrap.go` with a `cicffi.RunFlow`-backed `Actuator` behind the
  `ffi_cgo` build tag (nil / capability-absent in the default build), so a
  cgo-built relay serves the live capability end-to-end.
- **Audit on the deny path** (unchanged from the bridge doc).
