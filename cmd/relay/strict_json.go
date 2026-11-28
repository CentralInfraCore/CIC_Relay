package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Strict JSON reading of the wire files the admission trusts (review F1/F4). encoding/json alone accepts duplicate
// object keys (the last wins), matches field names case-insensitively and, without DisallowUnknownFields, ignores
// unknown keys: a producer and a consumer could then read the same bytes differently. strictScan walks the raw JSON
// once, before any struct decoding, and rejects unknown or differently spelled keys, duplicate keys, missing required
// keys, a non-object (or non-array) where one is required, and anything after the first value.
//
// This is deliberately the same algorithm as core/cabinet/abi_profile_strict.go (the #234 profile reader): the two
// are separate copies because cmd/relay is package main and the two changes live on separate branches. Folding both
// into one shared package is a follow-up once both have landed; until then the same negative cases are tested on
// both sides (see strict_json_test.go).

// jsonShape describes one JSON value.
type jsonShape struct {
	keys     map[string]*jsonShape // exact allowed keys of an object; a nil value is a leaf
	required []string              // keys that must be present
	elem     *jsonShape            // element shape, for arrays
}

// strictScan validates raw against the shape and rejects trailing data.
func strictScan(raw []byte, sh *jsonShape) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := scanJSONValue(dec, sh, "$"); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("unexpected data after the JSON value")
	}
	return nil
}

// scanJSONValue consumes one JSON value from dec against sh, rejecting what the shape does not allow.
func scanJSONValue(dec *json.Decoder, sh *jsonShape, path string) error {
	if sh == nil { // a leaf: any single JSON value, typed later by the struct decode
		var skip json.RawMessage
		return dec.Decode(&skip)
	}
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	delim, _ := tok.(json.Delim)
	if sh.elem != nil {
		if delim != '[' {
			return fmt.Errorf("%s: an array is required", path)
		}
		for i := 0; dec.More(); i++ {
			if err := scanJSONValue(dec, sh.elem, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	}
	if delim != '{' {
		return fmt.Errorf("%s: an object is required", path)
	}
	seen := map[string]bool{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		key, _ := kt.(string)
		if seen[key] {
			return fmt.Errorf("%s: duplicate key %q", path, clipForError(key))
		}
		seen[key] = true
		child, ok := sh.keys[key]
		if !ok {
			return fmt.Errorf("%s: unknown or differently spelled key %q", path, clipForError(key))
		}
		if err := scanJSONValue(dec, child, path+"."+key); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	for _, r := range sh.required {
		if !seen[r] {
			return fmt.Errorf("%s: required key %q is missing", path, r)
		}
	}
	return nil
}

// envelopeJSONShape is the exact wire shape of a ModuleReleaseEnvelope (module-release-envelope.schema.json).
var envelopeJSONShape = &jsonShape{
	keys: map[string]*jsonShape{
		"kind": nil, "envelope_version": nil, "module_id": nil, "wasm_sha256": nil, "executed_wasm_sha256": nil, "manifest_digest": nil,
		"signer": {keys: map[string]*jsonShape{"key_id": nil, "alg": nil}, required: []string{"key_id", "alg"}},
	},
	// envelope_version is checked right after the scan so that a missing or unknown version stays ENVELOPE_VERSION.
	required: []string{"kind", "module_id", "wasm_sha256", "executed_wasm_sha256", "manifest_digest", "signer"},
}

// authoritiesJSONShape is the exact wire shape of the module-sign authority bindings file.
var authoritiesJSONShape = &jsonShape{
	keys: map[string]*jsonShape{
		"kind": nil, "version": nil,
		"authorities": {elem: &jsonShape{keys: map[string]*jsonShape{"key_id": nil, "roles": nil}, required: []string{"key_id", "roles"}}},
	},
	required: []string{"kind", "version", "authorities"},
}
