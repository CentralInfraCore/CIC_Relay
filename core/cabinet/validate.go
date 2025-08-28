package cabinet

import (
	"crypto/sha256"
//	"crypto/x509"
	"encoding/json"
//	"encoding/pem"
//	"errors"
	"fmt"
)

// MetaCarrier interface: minden típus, ami validálható metaadatot tartalmaz, ezt implementálja.
type MetaCarrier interface {
	GetMeta() *BaseBlock
	GetData() any
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



func calculateChecksum(meta *BaseBlock, data any) (string, error) {
	payload := struct {
		Meta any `json:"meta"`
		Data any `json:"data"`
	}{
        Meta: metaForChecksum{
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


