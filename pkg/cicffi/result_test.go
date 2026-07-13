// No build tag: exercises the plain-Go result parser without the Rust staticlib.

package cicffi

import "testing"

func TestParseRunFlowResultSuccessWithAudit(t *testing.T) {
	raw := []byte(`{
		"ok": true,
		"status": 200,
		"body_base64": "cG9uZw==",
		"scope": ["db.read"],
		"audit": {
			"trace_id": "trace-abc-123",
			"method": "GET",
			"host": "127.0.0.1",
			"decision": "allow",
			"status": 200,
			"duration_ms": 7,
			"request_body_sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			"response_body_sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			"seq": 0,
			"record_hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		}
	}`)

	res, err := ParseRunFlowResult(raw)
	if err != nil {
		t.Fatalf("ParseRunFlowResult: %v", err)
	}
	if !res.OK {
		t.Fatalf("OK: got false, want true")
	}
	if res.Scope[0] != "db.read" {
		t.Fatalf("Scope: got %v", res.Scope)
	}
	if res.Audit == nil {
		t.Fatal("Audit: got nil, want populated")
	}
	a := res.Audit
	if a.TraceID != "trace-abc-123" {
		t.Errorf("TraceID: got %q", a.TraceID)
	}
	if a.Method != "GET" || a.Host != "127.0.0.1" || a.Decision != "allow" {
		t.Errorf("audit facts: %+v", a)
	}
	if a.Status == nil || *a.Status != 200 {
		t.Errorf("Status: got %v, want 200", a.Status)
	}
	if len(a.RecordHash) != 64 {
		t.Errorf("RecordHash: got %q (len %d), want 64-hex", a.RecordHash, len(a.RecordHash))
	}
}

func TestParseRunFlowResultFailureHasNoAudit(t *testing.T) {
	res, err := ParseRunFlowResult([]byte(`{"ok":false,"error":"host not allowed"}`))
	if err != nil {
		t.Fatalf("ParseRunFlowResult: %v", err)
	}
	if res.OK {
		t.Fatal("OK: got true, want false")
	}
	if res.Error != "host not allowed" {
		t.Errorf("Error: got %q", res.Error)
	}
	if res.Audit != nil {
		t.Errorf("Audit: got %+v, want nil on failure", res.Audit)
	}
}

func TestParseRunFlowResultRejectsGarbage(t *testing.T) {
	if _, err := ParseRunFlowResult([]byte("not json")); err == nil {
		t.Fatal("expected an error for non-JSON input")
	}
}
