package iac

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"centralrelay/pkg/canonicaljson"
)

// SpecDigest returns the canonical digest of a resource spec — the base of the
// proof-chain over IaC intent (feature-011, iac-object-model.md).
//
// The spec is first expanded to canonical [Field]s, so the compact `key: VALUE`
// and the fully-written `key: {value, default, mode}` descriptor produce the SAME
// bytes and therefore the SAME digest — the determinism contract the field model
// promises. Numbers are then normalized (spec #1): `4`, `4.0` and `4e0` digest
// identically, `-0` is `0`, and — critically — integers keep full precision (the
// bytes never round-trip through float64), so a value beyond float64's mantissa
// digests as its exact digits rather than the nearest double. The normalized model
// is serialized with the deterministic canonical-JSON encoder (sorted keys) and
// SHA-256'd.
func SpecDigest(spec map[string]interface{}) (string, error) {
	plain, err := toPlainValue(ExpandSpec(spec))
	if err != nil {
		return "", err
	}
	canonical, err := canonicaljson.ToJSON(normalizeNumbers(plain))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// toPlainValue round-trips a value through JSON into plain maps/slices, decoding
// numbers with UseNumber so integer precision survives (encoding/json's default
// would collapse them to float64). This also flattens struct values (the expanded
// [Field]s) into maps without triggering the canonical encoder's lossy struct path.
func toPlainValue(v interface{}) (interface{}, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out interface{}
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
