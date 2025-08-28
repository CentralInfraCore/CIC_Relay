package cabinet

import (
	"testing"
)

// --- ValidateMeta ---

func TestValidateMeta_SetsChecksumAndValid(t *testing.T) {
	meta := &BaseBlock{
		Name:        "n",
		Version:     "v",
		Description: "d",
		CreatedBy:   "u",
		Timestamp:   "t",
	}
	payload := struct {
		A int      `json:"a"`
		L []string `json:"l"`
	}{A: 42, L: []string{"x", "y"}}

	want, err := calculateChecksum(meta, payload)
	if err != nil {
		t.Fatalf("pre-calc failed: %v", err)
	}

	if err := ValidateMeta(meta, payload); err != nil {
		t.Fatalf("ValidateMeta err: %v", err)
	}
	if meta.Checksum != want {
		t.Fatalf("Checksum mismatch: got %q want %q", meta.Checksum, want)
	}
	if !meta.Valid {
		t.Fatalf("Valid should be true")
	}
}

func TestValidateMeta_MissingRequiredFields_ReturnsValidationError(t *testing.T) {
	meta := &BaseBlock{} // Name és Version hiányzik
	payload := struct{ X string }{"ok"}

	err := ValidateMeta(meta, payload)
	if err == nil {
		t.Fatalf("expected ValidationError, got nil")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	// mindkét kötelező mező szerepeljen a hibák között
	fields := map[string]bool{}
	for _, c := range ve.Cause {
		fields[c.Field] = true
	}
	if !fields["meta.name"] || !fields["meta.version"] {
		t.Fatalf("missing causes for name/version: %+v", ve.Cause)
	}
	// checksum kiszámolódik, de Valid marad false
	if meta.Checksum == "" {
		t.Fatalf("Checksum should be set even when ValidationError occurs")
	}
	if meta.Valid {
		t.Fatalf("Valid should be false on ValidationError")
	}
}

func TestValidateMeta_ChecksumComputeFailure_ReturnsValidationError(t *testing.T) {
	meta := &BaseBlock{Name: "n", Version: "v"}
	type bad struct {
		C chan int `json:"c"`
	}
	payload := bad{C: make(chan int)}

	err := ValidateMeta(meta, payload)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if _, ok := err.(*ValidationError); !ok {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	// checksum ne legyen beállítva, Valid maradjon false
	if meta.Checksum != "" {
		t.Fatalf("Checksum should be empty on compute failure, got %q", meta.Checksum)
	}
	if meta.Valid {
		t.Fatalf("Valid should be false on compute failure")
	}
}

// --- calculateChecksum (belső részletek) ---

func TestCalculateChecksum_IgnoresVolatileMetaFields(t *testing.T) {
	meta := &BaseBlock{
		Name:        "n",
		Version:     "v",
		Description: "d",
		CreatedBy:   "u",
		Timestamp:   "t",
		Signature:   "sig1",
		Certificate: "cert1",
		CRLRef:      "ocsp1",
		Valid:       true,
	}
	payload := struct{ P int }{P: 1}

	c1, err := calculateChecksum(meta, payload)
	if err != nil {
		t.Fatalf("calculateChecksum err: %v", err)
	}

	// csak a volatilis mezőket változtatjuk
	meta.Signature = "sig2"
	meta.Certificate = "cert2"
	meta.CRLRef = "ocsp2"
	meta.Valid = false

	c2, err := calculateChecksum(meta, payload)
	if err != nil {
		t.Fatalf("calculateChecksum err: %v", err)
	}
	if c1 != c2 {
		t.Fatalf("checksum changed after volatile-field mutation: %q -> %q", c1, c2)
	}
}

func TestCalculateChecksum_ChangesWhenStableMetaOrDataChanges(t *testing.T) {
	meta := &BaseBlock{
		Name:        "n",
		Version:     "v",
		Description: "d",
		CreatedBy:   "u",
		Timestamp:   "t",
	}
	payload := struct{ P int }{P: 1}

	c1, err := calculateChecksum(meta, payload)
	if err != nil {
		t.Fatalf("calculateChecksum err: %v", err)
	}

	// stabil meta mező változik → checksum változik
	meta.Description = "d2"
	c2, _ := calculateChecksum(meta, payload)
	if c1 == c2 {
		t.Fatalf("checksum did not change after stable meta change")
	}

	// payload változik → checksum változik
	meta.Description = "d" // vissza
	payload.P = 2
	c3, _ := calculateChecksum(meta, payload)
	if c1 == c3 {
		t.Fatalf("checksum did not change after payload change")
	}
}
