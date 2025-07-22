package cabinet

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

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

	// 4. TODO: Signature és CertificateSignature validálása

	meta.Valid = true
	return nil
}