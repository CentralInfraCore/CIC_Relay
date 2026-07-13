package httpcap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

const okResult = `{
	"ok": true, "status": 200, "body_base64": "cG9uZw==", "scope": ["db.read"],
	"audit": { "trace_id": "t-1", "method": "GET", "host": "127.0.0.1",
		"decision": "allow", "status": 200, "duration_ms": 5,
		"request_body_sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"response_body_sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		"seq": 0,
		"record_hash": "3b1f9c0a7e5d2b48f6a1c3e59d7042b8c1f6a9d3e0b4278c5a1f9e3d6b0c2748f" }
}`

func TestRequestFunc_SuccessReturnsNonSecretResult(t *testing.T) {
	var gotReq []byte
	fn := NewRequestFunc(ActuatorFunc(func(reqJSON []byte) ([]byte, error) {
		gotReq = reqJSON
		return []byte(okResult), nil
	}))

	input := map[string]any{
		"subject": "relay.worker", "scope": []string{"db.read"},
		"secret": "hvs.TOPSECRET", "target": "http://127.0.0.1/x",
		"allow_hosts": []string{"127.0.0.1"}, "insecure": true,
	}
	out, err := fn(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The request the actuator saw carried the caller's fields through.
	if !json.Valid(gotReq) {
		t.Fatalf("actuator request is not valid JSON: %q", gotReq)
	}

	res, ok := out.(Result)
	if !ok {
		t.Fatalf("output type: got %T, want Result", out)
	}
	if res.Status != 200 || res.BodyBase64 != "cG9uZw==" {
		t.Errorf("result: %+v", res)
	}
	if len(res.Scope) != 1 || res.Scope[0] != "db.read" {
		t.Errorf("scope: %v", res.Scope)
	}
	if res.Audit == nil || res.Audit.RecordHash == "" || res.Audit.Decision != "allow" {
		t.Errorf("audit: %+v", res.Audit)
	}

	// The secret never appears in the capability output.
	outJSON, _ := json.Marshal(res)
	if bytes.Contains(outJSON, []byte("TOPSECRET")) {
		t.Fatal("secret leaked into the capability result")
	}
}

func TestRequestFunc_RefusedActuationIsAnError(t *testing.T) {
	fn := NewRequestFunc(ActuatorFunc(func([]byte) ([]byte, error) {
		return []byte(`{"ok":false,"error":"host not allowed"}`), nil
	}))
	_, err := fn(context.Background(), map[string]any{"target": "http://10.0.0.1/x"})
	if err == nil {
		t.Fatal("expected an error for a refused actuation")
	}
}

func TestRequestFunc_NilActuator(t *testing.T) {
	fn := NewRequestFunc(nil)
	_, err := fn(context.Background(), map[string]any{})
	if !errors.Is(err, ErrNoActuator) {
		t.Fatalf("got %v, want ErrNoActuator", err)
	}
}

func TestRequestFunc_ActuatorErrorPropagates(t *testing.T) {
	fn := NewRequestFunc(ActuatorFunc(func([]byte) ([]byte, error) {
		return nil, errors.New("bridge down")
	}))
	_, err := fn(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected the actuator error to surface")
	}
}

func TestRequestFunc_GarbageResultIsAnError(t *testing.T) {
	fn := NewRequestFunc(ActuatorFunc(func([]byte) ([]byte, error) {
		return []byte("not json"), nil
	}))
	_, err := fn(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected an error parsing a garbage actuation result")
	}
}

func TestRequestFunc_UnmarshalableInputIsAnError(t *testing.T) {
	fn := NewRequestFunc(ActuatorFunc(func([]byte) ([]byte, error) {
		return []byte(okResult), nil
	}))
	// A channel cannot be JSON-marshaled → the request cannot be built.
	_, err := fn(context.Background(), make(chan int))
	if err == nil {
		t.Fatal("expected an error marshaling an unmarshalable input")
	}
}
