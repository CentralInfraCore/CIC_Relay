package obs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"centralrelay/pkg/canonicaljson"
)

// RecordHash returns the hex-encoded SHA-256 of the canonical JSON of v.
// Use this to fill IntegrityEnvelope.RecordHash.
func RecordHash(v any) (string, error) {
	b, err := canonicaljson.ToJSON(v)
	if err != nil {
		return "", fmt.Errorf("obs: RecordHash: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// GlobalChainHash returns a single SHA-256 over all records in emission order.
// It concatenates individual RecordHashes with \x00 separators so that
// adjacent record hashes cannot collide.
func GlobalChainHash(records []any) (string, error) {
	var sb strings.Builder
	for _, r := range records {
		h, err := RecordHash(r)
		if err != nil {
			return "", fmt.Errorf("obs: GlobalChainHash: %w", err)
		}
		sb.WriteString(h)
		sb.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:]), nil
}

// TypeChainHash returns a SHA-256 scoped to a named record type.
// The typeName is prepended (with a \x00 separator) before the record hashes,
// so chains from different types cannot collide even if they contain identical records.
func TypeChainHash(typeName string, records []any) (string, error) {
	var sb strings.Builder
	sb.WriteString(typeName)
	sb.WriteByte(0)
	for _, r := range records {
		h, err := RecordHash(r)
		if err != nil {
			return "", fmt.Errorf("obs: TypeChainHash(%s): %w", typeName, err)
		}
		sb.WriteString(h)
		sb.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:]), nil
}
