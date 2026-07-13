# FFI audit bridge — actuation audit → Go ProofTrace (feature-009)

Status: **implemented** (`cic-ffi` run_flow, `pkg/cicffi`, `core/cabinet`). The
first span of the bridge from the Rust-side audit chain to the running Go relay:
an actuation's non-secret audit record now **crosses the FFI seam** and folds
into the Go relay's `ProofTrace` vocabulary.

## The span

```
http-executor actuation
  └─ HashChainedAuditSink → ChainedEntry (record + record_hash)   [Rust]
      └─ run_flow emits it as the outcome's `audit` object        [FFI, ABI v3]
          └─ ParseRunFlowResult → RunFlowResult.Audit             [Go, pkg/cicffi]
              └─ ProofTraceStepFromActuation → ProofTraceStep     [Go, core/cabinet]
                  └─ ComputeChainHashV1                           [existing ProofTrace]
```

## Rust side (`cic-ffi`)

`run_flow` attaches a `HashChainedAuditSink` to the `HttpExecutor` in the shared
`actuate` path (so both the mock and live profiles get it), captures the head
`ChainedEntry`, and adds an `audit` object to the success outcome:

```json
{ "ok": true, "status": 200, "body_base64": "...", "scope": ["db.read"],
  "audit": { "trace_id": "...", "method": "GET", "host": "127.0.0.1",
             "decision": "allow", "status": 200, "duration_ms": 7,
             "request_body_sha256": "...", "response_body_sha256": "...",
             "seq": 0, "record_hash": "..." } }
```

The request may carry a `trace_id`; it is applied via `ExecRequest::with_trace_id`
and echoed into the audit so the Go relay can bind the actuation to its own
`ProofTrace`. The `audit` object carries **only digests and facts** — never the
credential, the `Authorization` header, a Vault token, or any body contents. The
outcome shape changed, so the C-ABI version is bumped **v2 → v3** (Go
`ExpectedABIVersion` kept in sync, `CheckABI` guards a mismatch).

## Go side

- `pkg/cicffi/result.go` (no build tag — plain Go, testable without the
  staticlib): `ActuationAudit`, `RunFlowResult`, `ParseRunFlowResult`.
- `core/cabinet.ProofTraceStepFromActuation(traceID, method, host,
  requestBodySHA256, responseBodySHA256)` maps the actuation audit into a
  `ProofTraceStep` (body digests → input/output hashes; name = trace id, or
  `"<method> <host>"` when none). An actuation now folds into the **same**
  hash-chained `ProofTrace` as native and WASM steps.

## Tested

- Rust: `run_flow` happy path asserts the `audit` object (trace_id round-trip,
  `record_hash` 64-hex, decision/method/host/status); a unit test covers the
  `deny:<reason>` projection. cic-ffi coverage 98.6%, mutation 0 missed.
- Go (non-cgo): `ParseRunFlowResult` success/failure/garbage;
  `ProofTraceStepFromActuation` mapping + chain-hash fold.
- Go (cgo, `make test-ffi`): the live Go→Rust→Go path parses the audit and maps
  it into a `ProofTraceStep` + `ComputeChainHashV1`, and re-asserts the secret
  never crosses back.

## Persistence — the recorder span (done)

An actuation's audit is now **durably recordable**. `core/cabinet`:

- `ProofFromActuation(workflowID, sourceDigest, timestamp, recordHash, step)`
  wraps a single actuation step into an `ActuationProof` — a complete, recordable
  single-step `ProofTrace` (with its `ChainHash`) **plus** the Rust-side
  tamper-evident `record_hash` anchor. Both chains travel together.
- Handed to the existing `ExecutionRecorder` (`WorkflowRecorder` →
  `GitStateRecorder`), it is persisted as a **Vault-signed Git commit**: the
  `actual.json` carries the `ProofTrace` `ChainHash` and the `record_hash`, and
  the manifest hash is signed via `crypto.Sign` (Vault Transit).

An integration test (`core/nexus/recorder/actuation_proof_test.go`) proves the
full durable path against a temp Git repo + a mock signer; gremlins efficacy on
`core/cabinet` stays ≥ 90% (no survivors in `proof_trace.go`).

## Deny-path audit (done)

A refused actuation is no longer silent. `run_flow` threads the executor's audit
entry through the error path (the executor records on **both** the allow and
deny paths), so a policy-denied / SSRF-blocked / limited actuation returns
`{"ok":false,"error":...,"audit":{...,"decision":"deny:..."}}` — the deny audit
carries the same tamper-evident `record_hash` as a success. A failure *before*
the actuation (countersign/seal/open, or an unknown profile) carries no audit,
since nothing was actuated. Proven by a Rust `run_flow` deny test, a Go
`ParseRunFlowResult` deny test, and a cgo end-to-end deny test (`make test-ffi`).

## Still open (deliberate)

- **Relay-driven actuation** is now live via the `http.request` capability (see
  `http-request-capability.md`); its remaining deploy span is the bootstrap +
  cgo actuator wiring.
- **Deny audit through the capability**: a policy-denied capability call fails
  the workflow step (the actuation is refused), so `Setx` aborts without
  recording the deny `ProofTrace`. Surfacing the deny audit as a recorded
  (failed) step — rather than only in the `run_flow` result — is a follow-up.
