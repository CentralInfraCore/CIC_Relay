package cabinet_test

import (
	"testing"

	"centralrelay/core/cabinet"
)

// helper a FieldError-ok könnyebb ellenőrzéséhez
func toMap(causes []cabinet.FieldError) map[string]string {
	m := make(map[string]string, len(causes))
	for _, c := range causes {
		m[c.Field] = c.Msg
	}
	return m
}

func TestValidateMeta_API_SuccessSetsChecksumAndValid(t *testing.T) {
	meta := &cabinet.BaseBlock{
		Name:        "schema-x",
		Version:     "1.0.0",
		Description: "desc",
		CreatedBy:   "tester",
		Timestamp:   "2025-08-27T00:00:00Z",
	}
	// determinisztikus, jól szerializálható payload
	payload := struct {
		Type    string   `json:"type"`
		Accepts []string `json:"accepts"`
	}{
		Type:    "executor",
		Accepts: []string{"a", "b"},
	}

	if err := cabinet.ValidateMeta(meta, payload); err != nil {
		t.Fatalf("ValidateMeta() err = %v, want nil", err)
	}
	if meta.Checksum == "" {
		t.Fatalf("Checksum not set")
	}
	if !meta.Valid {
		t.Fatalf("Valid not set to true")
	}
}

func TestValidateMeta_API_DeterministicAcrossEqualInputs(t *testing.T) {
	meta1 := &cabinet.BaseBlock{Name: "n", Version: "v", Description: "d", CreatedBy: "u", Timestamp: "t"}
	meta2 := &cabinet.BaseBlock{Name: "n", Version: "v", Description: "d", CreatedBy: "u", Timestamp: "t"}
	payload := struct {
		T string   `json:"t"`
		S []string `json:"s"`
	}{T: "x", S: []string{"y", "z"}}

	if err := cabinet.ValidateMeta(meta1, payload); err != nil {
		t.Fatalf("ValidateMeta(meta1) err = %v", err)
	}
	if err := cabinet.ValidateMeta(meta2, payload); err != nil {
		t.Fatalf("ValidateMeta(meta2) err = %v", err)
	}
	if meta1.Checksum != meta2.Checksum {
		t.Fatalf("Checksum mismatch for identical inputs: %q vs %q", meta1.Checksum, meta2.Checksum)
	}
}

func TestValidateMeta_API_DataChangeAltersChecksum(t *testing.T) {
	meta := &cabinet.BaseBlock{Name: "n", Version: "v"}
	payload := struct {
		A int      `json:"a"`
		L []string `json:"l"`
	}{A: 1, L: []string{"p", "q"}}

	if err := cabinet.ValidateMeta(meta, payload); err != nil {
		t.Fatalf("first ValidateMeta() err = %v", err)
	}
	first := meta.Checksum

	// módosítsuk a payloadot és számoljuk újra
	payload.L = append(payload.L, "r")
	if err := cabinet.ValidateMeta(meta, payload); err != nil {
		t.Fatalf("second ValidateMeta() err = %v", err)
	}
	if meta.Checksum == first {
		t.Fatalf("Checksum did not change after payload mutation")
	}
}

func TestValidateMeta_API_VolatileMetaDoesNotAffectChecksum(t *testing.T) {
	meta := &cabinet.BaseBlock{
		Name:      "n",
		Version:   "v",
		Signature: "sig-1",
		// Certificate/CRLRef/Valid is not part of checksum either
	}
	payload := struct{ X string }{X: "v"}

	if err := cabinet.ValidateMeta(meta, payload); err != nil {
		t.Fatalf("first ValidateMeta() err = %v", err)
	}
	first := meta.Checksum

	// változtassunk olyan mezőkön, amik nem számítanak a checksumba
	meta.Signature = "sig-2"
	meta.Certificate = "cert-xyz"
	meta.CRLRef = "ocsp://example"
	// Valid-ot is átírjuk kézzel, hogy lássuk nem zavar
	meta.Valid = false

	if err := cabinet.ValidateMeta(meta, payload); err != nil {
		t.Fatalf("second ValidateMeta() err = %v", err)
	}
	if meta.Checksum != first {
		t.Fatalf("Checksum changed after touching volatile meta fields: %q -> %q", first, meta.Checksum)
	}
	if !meta.Valid {
		t.Fatalf("Valid should be true on success")
	}
}

func TestValidateMeta_API_MissingNameAndVersion_YieldsValidationError(t *testing.T) {
	meta := &cabinet.BaseBlock{} // Name és Version üres
	payload := struct{ X int }{X: 1}

	err := cabinet.ValidateMeta(meta, payload)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	ve, ok := err.(*cabinet.ValidationError)
	if !ok {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	m := toMap(ve.Cause)
	if m["meta.name"] != "required" || m["meta.version"] != "required" {
		t.Fatalf("unexpected causes: %+v", m)
	}
	// Checksum ettől még kiszámolódik, de Valid marad false
	if meta.Checksum == "" {
		t.Fatalf("Checksum should still be set even on ValidationError (name/version missing)")
	}
	if meta.Valid {
		t.Fatalf("Valid should be false on ValidationError")
	}
}

func TestValidateMeta_API_ChecksumComputeFailure_YieldsValidationError(t *testing.T) {
	meta := &cabinet.BaseBlock{Name: "n", Version: "v"}
	// Nem szerializálható mező a payloadban → json.Marshal error
	type bad struct {
		C chan int `json:"c"`
	}
	payload := bad{C: make(chan int)}

	err := cabinet.ValidateMeta(meta, payload)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	ve, ok := err.(*cabinet.ValidationError)
	if !ok {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	m := toMap(ve.Cause)
	msg, ok := m["checksum"]
	if !ok {
		t.Fatalf("expected checksum cause, got: %+v", m)
	}
	if msg == "" {
		t.Fatalf("checksum cause message should not be empty")
	}
	// Sikertelen checksum számításnál ne állítson be digestet
	if meta.Checksum != "" {
		t.Fatalf("Checksum should be empty on compute failure, got %q", meta.Checksum)
	}
	if meta.Valid {
		t.Fatalf("Valid should be false on compute failure")
	}
}
