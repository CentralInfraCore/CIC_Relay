// Package wasmcanon is the single, reusable implementation of the executed-identity transformation wasm-canon/v1
// (O-1 INT-03, #162 section 5a).
//
// A release names two byte strings of its module: the raw wasm and the bytes that are actually compiled (the
// "executed" wasm). The second is derived from the first by this transformation: decode the module with the pinned
// wabin and CoreFeaturesV2, drop every custom section, encode it again. It is NOT a bare section removal: the encoder
// normalises (for example LEB128 sizes), so the executed digest depends on the pinned wabin and on these features.
// Producer and consumer must therefore call the SAME code; before this package the relay carried the function inline
// in core/cabinet and a producer had no way to obtain the value except by reimplementing it.
//
// The transformation itself is unchanged: this is the former core/cabinet function moved, with the same behaviour
// and the same error text, so every existing checksum and executed digest stays valid (Checksum() is untouched).
// What the package does NOT claim: it is not an implementation-independent canonicalisation specification. It is bound
// to the pinned wabin (see the TransformID and the build information the tool prints), and a second implementation that
// does not use the same library can differ on inputs the library normalises.
package wasmcanon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/wasm"
)

// TransformID names the transformation in releases, envelopes and evidence.
const TransformID = "wasm-canon/v1"

// Canonicalize returns the executed representation of a raw wasm module: the module decoded with wabin under
// CoreFeaturesV2 (which also validates it), its custom sections dropped (this includes the non-deterministic "name"
// and "producers" sections), and encoded again.
func Canonicalize(raw []byte) ([]byte, error) {
	module, err := binary.DecodeModule(raw, wasm.CoreFeaturesV2)
	if err != nil {
		return nil, fmt.Errorf("failed to decode wasm module with wabin: %w", err)
	}
	module.CustomSections = nil
	return binary.EncodeModule(module), nil
}

// Digest is the sha256:<lowercase hex> form every digest of the O-1 wire contracts uses.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ExecutedDigest is Digest(Canonicalize(raw)): the executed_wasm_sha256 of a release envelope.
func ExecutedDigest(raw []byte) (string, error) {
	exec, err := Canonicalize(raw)
	if err != nil {
		return "", err
	}
	return Digest(exec), nil
}
