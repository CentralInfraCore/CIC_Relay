package cabinet

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
)

// ID visszaadja a séma egyedi azonosítóját a név és verzió alapján.
func (s *Schema) ID() string {
	return s.Meta.Name + "@" + s.Meta.Version
}

// Checksum újraszámolja a sémára vonatkozó hash-t a Meta és Spec mező alapján.
func (s *Schema) Checksum() (string, error) {
	data := struct {
		Meta BaseBlock  `json:"meta"`
		Spec SchemaDef  `json:"spec"`
	}{
		Meta: BaseBlock{
			Name:        s.Meta.Name,
			Version:     s.Meta.Version,
			Description: s.Meta.Description,
			CreatedBy:   s.Meta.CreatedBy,
			Timestamp:   s.Meta.Timestamp,
		},
		Spec: s.Spec,
	}
	buf, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(buf)
	return fmt.Sprintf("%x", hash), nil
}

// Validate ellenőrzi a kötelező mezők meglétét, újraszámolja a checksumot, ellenőrzi a signature-t és frissíti a Valid mezőt.
func (s *Schema) Validate() error {
	if s.Meta.Name == "" || s.Meta.Version == "" {
		s.Meta.Valid = false
		return fmt.Errorf("missing name or version")
	}
	calc, err := s.Checksum()
	if err != nil {
		s.Meta.Valid = false
		return fmt.Errorf("checksum error: %w", err)
	}
	if calc != s.Meta.Checksum {
		s.Meta.Valid = false
		return fmt.Errorf("checksum mismatch")
	}

	// Tanúsítvány dekódolás
	block, _ := pem.Decode([]byte(s.Meta.Certificate))
	if block == nil {
		s.Meta.Valid = false
		return errors.New("invalid certificate PEM format")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		s.Meta.Valid = false
		return fmt.Errorf("invalid certificate: %w", err)
	}

	// TODO: CertificateSignature validálása trusted root-tal szemben
	// TODO: Signature validálása cert.PublicKey ellen

	s.Meta.Valid = true
	return nil
}
