package cabinet

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
)

// MetaCarrier interface: minden típus, ami validálható metaadatot tartalmaz, ezt implementálja.
type MetaCarrier interface {
	GetMeta() *BaseBlock
	GetData() any
}

func ValidateMeta(meta *BaseBlock, data any) error {
	// 1. Kötelező mezők
	if meta.Name == "" || meta.Version == "" {
		meta.Valid = false
		return fmt.Errorf("missing name or version")
	}

	// 2. Checksum újraszámolása
	calc, err := calculateChecksum(meta, data)
	if err != nil {
		meta.Valid = false
		return fmt.Errorf("checksum error: %w", err)
	}
	if calc != meta.Checksum {
		meta.Valid = false
		return fmt.Errorf("checksum mismatch")
	}

	// 3. Certificate parse
	block, _ := pem.Decode([]byte(meta.Certificate))
	if block == nil {
		meta.Valid = false
		return errors.New("invalid certificate PEM format")
	}
	_, err = x509.ParseCertificate(block.Bytes)
	if err != nil {
		meta.Valid = false
		return fmt.Errorf("certificate parse error: %w", err)
	}
// ValidateMeta performs common validation for BaseBlock + payload and updates Checksum/Valid.
// Signature verification is intentionally left for later (policy-dependent).
func ValidateMeta(meta *BaseBlock, data any) error {
    var errs []FieldError
    if meta.Name == "" {
        errs = append(errs, FieldError{Field: "meta.name", Msg: "required"})
    }
    if meta.Version == "" {
        errs = append(errs, FieldError{Field: "meta.version", Msg: "required"})
    }
    // compute and set checksum deterministically
    digest, err := calculateChecksum(meta, data)
    if err != nil {
        errs = append(errs, FieldError{Field: "checksum", Msg: "compute failed: " + err.Error()})
    } else {
        meta.Checksum = digest
    }
    if len(errs) > 0 {
        return &ValidationError{Cause: errs}
    }
    meta.Valid = true
    return nil
}


	// 4. TODO: Signature és CertificateSignature validálása

	meta.Valid = true
	return nil
}

func calculateChecksum(meta *BaseBlock, data any) (string, error) {
	payload := struct {
		Meta any `json:"meta"`
		Data any `json:"data"`
	}{
		Meta: struct {
			Name        string `json:"name"`
			Version     string `json:"version"`
			Description string `json:"description"`
			CreatedBy   string `json:"created_by"`
			Timestamp   string `json:"timestamp"`
		}{
			Name:        meta.Name,
			Version:     meta.Version,
			Description: meta.Description,
			CreatedBy:   meta.CreatedBy,
			Timestamp:   meta.Timestamp,
		},
		Data: data,
	}

	buf, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(buf)
	return fmt.Sprintf("%x", hash), nil
}

// schema.go
func (s *Schema) Validate() error {
	return ValidateMeta(&s.Meta, s.Body) // Body: []byte / parsed JSON – ami nálad van
}

// ModuleDescriptor.go
func (m *ModuleDescriptor) Validate() error {
	return ValidateMeta(&m.Meta, m.GetData()) // determinisztikus nézet
}

// workflow.go
func (w *Workflow) Validate() error {
	return ValidateMeta(&w.Meta, w.Steps) // Steps: a te struktúrád
}