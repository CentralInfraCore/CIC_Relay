package iac

import (
	"crypto/sha256"
	"encoding/hex"

	"centralrelay/pkg/canonicaljson"
)

// SpecDigest returns the canonical digest of a resource spec — the base of the
// proof-chain over IaC intent (feature-011, iac-object-model.md).
//
// The spec is first expanded to canonical [Field]s, so the compact `key: VALUE`
// and the fully-written `key: {value, default, mode}` descriptor produce the SAME
// bytes and therefore the SAME digest — the determinism contract the field model
// promises. The expanded model is then serialized with the deterministic
// canonical-JSON encoder (sorted keys) and SHA-256'd.
//
// NOTE: number normalization (1 vs 1.0 vs 1e0, -0) is NOT yet applied — a value's
// Go type is preserved as written, so `cpu: 4` and `cpu: 4.0` currently digest
// differently. Pinning that is CIC Canonical Object Encoding v1 (spec #1), which
// must ship with language-neutral Go+Rust test vectors; until then, write numbers
// consistently.
func SpecDigest(spec map[string]interface{}) (string, error) {
	canonical, err := canonicaljson.ToJSON(ExpandSpec(spec))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
