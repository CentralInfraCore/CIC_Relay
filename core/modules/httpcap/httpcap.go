// Package httpcap implements the relay's `http.request` capability: a native
// cabinet module that performs an authenticated HTTP actuation through the
// feature-008/009 trust-flow. Registered in the cabinet, a workflow step like
// `http@1.0.request(input)` drives a real actuation; because it is a native
// step, Setx hashes its input/output into the ProofTrace and the recorder
// persists it — so a capability call is auditable and durable exactly like any
// native or WASM step, with no special-casing.
//
// The actuation itself is performed by an injected [Actuator] whose single
// method matches the Rust FFI `run_flow` seam byte-for-byte (JSON in, JSON out).
// The cgo relay build injects a `cicffi.RunFlow`-backed actuator; the cabinet and
// this package never import cgo, so the default build stays pure-Go.
package httpcap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Actuator performs one HTTP actuation via the trust-flow. reqJSON and resJSON
// are the Rust `run_flow` request/result documents. The production implementation
// is the cgo FFI (`cicffi.RunFlow`), injected at relay startup so the cabinet
// never hard-depends on cgo.
type Actuator interface {
	RunFlow(reqJSON []byte) (resJSON []byte, err error)
}

// ActuatorFunc adapts a plain function to the [Actuator] interface (mirrors the
// http.HandlerFunc idiom), so `cicffi.RunFlow` or a test double can be used
// directly.
type ActuatorFunc func(reqJSON []byte) ([]byte, error)

// RunFlow calls f.
func (f ActuatorFunc) RunFlow(reqJSON []byte) ([]byte, error) { return f(reqJSON) }

// Audit is the non-secret audit record an actuation carries back — the Rust
// ChainedEntry projection: body digests, the allow/deny decision, and the
// tamper-evident record_hash. It never carries the credential, the Authorization
// header, a token, or any body contents.
type Audit struct {
	TraceID            string `json:"trace_id"`
	Method             string `json:"method"`
	Host               string `json:"host"`
	Decision           string `json:"decision"`
	Status             *int   `json:"status"`
	DurationMs         uint64 `json:"duration_ms"`
	RequestBodySHA256  string `json:"request_body_sha256"`
	ResponseBodySHA256 string `json:"response_body_sha256"`
	Seq                uint64 `json:"seq"`
	RecordHash         string `json:"record_hash"`
}

// Result is the capability step's output: the non-secret outcome of an
// actuation. The opened credential never appears here — only the status, the
// (base64) response body, the countersigned scope, and the audit record.
type Result struct {
	Status     int      `json:"status"`
	BodyBase64 string   `json:"body_base64"`
	Scope      []string `json:"scope"`
	Audit      *Audit   `json:"audit,omitempty"`
}

// runFlowResult is the wire form of the actuator's JSON output.
type runFlowResult struct {
	OK         bool     `json:"ok"`
	Status     int      `json:"status"`
	BodyBase64 string   `json:"body_base64"`
	Scope      []string `json:"scope"`
	Audit      *Audit   `json:"audit"`
	Error      string   `json:"error"`
}

// ErrNoActuator is returned by the capability when no [Actuator] is configured —
// e.g. a relay built without the `ffi_cgo` bridge. It lets the workflow step fail
// cleanly rather than panic.
var ErrNoActuator = errors.New("httpcap: no actuator configured (build the relay with the ffi_cgo bridge)")

// NewRequestFunc returns the native cabinet function for the `request` operation,
// backed by actuator. The returned function matches the native module ABI
// `func(context.Context, interface{}) (interface{}, error)`: it marshals the
// step input as the `run_flow` request, actuates, and returns the non-secret
// [Result].
//
// A flow that runs but is refused (policy deny, SSRF block, limit, transport
// error) is surfaced as an error so the workflow step fails; the credential and
// any body contents never cross into the result.
func NewRequestFunc(actuator Actuator) func(context.Context, interface{}) (interface{}, error) {
	return func(_ context.Context, input interface{}) (interface{}, error) {
		if actuator == nil {
			return nil, ErrNoActuator
		}
		reqJSON, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("httpcap: marshal request: %w", err)
		}
		resJSON, err := actuator.RunFlow(reqJSON)
		if err != nil {
			return nil, fmt.Errorf("httpcap: actuation failed: %w", err)
		}
		var res runFlowResult
		if err := json.Unmarshal(resJSON, &res); err != nil {
			return nil, fmt.Errorf("httpcap: parse actuation result: %w", err)
		}
		if !res.OK {
			return nil, fmt.Errorf("httpcap: actuation refused: %s", res.Error)
		}
		return Result{
			Status:     res.Status,
			BodyBase64: res.BodyBase64,
			Scope:      res.Scope,
			Audit:      res.Audit,
		}, nil
	}
}
