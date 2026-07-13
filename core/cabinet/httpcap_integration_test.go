package cabinet

import (
	"bytes"
	"context"
	"testing"

	"centralrelay/core/modules/httpcap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetx_HTTPRequestCapability proves the feature-009 relay-integration span:
// the `http.request` capability, registered as a native cabinet module, is driven
// by Setx as an ordinary workflow step — so its actuation folds into the same
// hash-chained ProofTrace as any native/WASM step, with no special-casing. The
// actuation is served by a mock Actuator (the real one is the cgo FFI, injected
// at relay startup).
func TestSetx_HTTPRequestCapability(t *testing.T) {
	ctx := context.Background()

	const actuationResult = `{
		"ok": true, "status": 200, "body_base64": "cG9uZw==", "scope": ["db.read"],
		"audit": { "trace_id": "cap-1", "method": "GET", "host": "127.0.0.1",
			"decision": "allow", "status": 200, "duration_ms": 5,
			"request_body_sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			"response_body_sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			"seq": 0,
			"record_hash": "3b1f9c0a7e5d2b48f6a1c3e59d7042b8c1f6a9d3e0b4278c5a1f9e3d6b0c2748f" }
	}`

	var sawSecret bool
	actuator := httpcap.ActuatorFunc(func(reqJSON []byte) ([]byte, error) {
		// The credential travels only Relay→Rust; it is never stored in the trace.
		sawSecret = bytes.Contains(reqJSON, []byte("hvs.TOPSECRET"))
		return []byte(actuationResult), nil
	})

	modStore := NewMemModuleStore()
	wfStore := NewMemWorkflowStore()

	native := NativeFunctions{"request": httpcap.NewRequestFunc(actuator)}
	mod, err := NewModuleDescriptor(ctx, BaseBlock{Name: "http", Version: "1.0"}, "native", nil, nil, nil)
	require.NoError(t, err)
	mod.NativeImpl = native
	modStore.Put(mod)

	wf, err := NewWorkflow(ctx, WorkflowIdentity{Version: "1.0"},
		WorkflowConfig{Name: "wf.http", Steps: []string{"http@1.0.request(input)"}}, WorkflowSigning{})
	require.NoError(t, err)
	wfStore.Put(wf)

	fixedTime := "2026-07-13T00:00:00Z"
	svc := &cabinetService{
		modules:     modStore,
		workflows:   wfStore,
		wasmManager: func() wasmManager { return new(MockWasmManager) }(),
		clock:       func() string { return fixedTime },
	}

	resp, err := svc.Setx(ctx, &SetPayload{
		WorkflowID: wf.ID(),
		Payload: map[string]any{
			"subject": "relay.worker", "scope": []string{"db.read"},
			"secret": "hvs.TOPSECRET", "target": "http://127.0.0.1/x",
			"allow_hosts": []string{"127.0.0.1"}, "insecure": true,
		},
	})
	require.NoError(t, err)
	require.True(t, sawSecret, "the credential must reach the actuator (Relay→Rust)")

	// The capability actuation produced exactly one ProofTrace step, chained.
	require.NotNil(t, resp.Trace)
	require.Len(t, resp.Trace.Steps, 1)
	step := resp.Trace.Steps[0]
	assert.Equal(t, "http@1.0", step.Module)
	assert.NotEmpty(t, step.InputHash)
	assert.NotEmpty(t, step.OutputHash)
	assert.Equal(t, ComputeChainHashV1(resp.Trace.WorkflowID, "", resp.Trace.Steps), resp.Trace.ChainHash)

	// The opened credential never reaches the ProofTrace (only hashes/outcome).
	assert.NotContains(t, resp.Trace.Steps[0].OutputHash, "TOPSECRET")
}
