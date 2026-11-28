package cabinet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// This file is the strict wire reader of the ABI-profile artifact (review F4).
// encoding/json alone accepts duplicate object keys (last wins), matches field
// names case-insensitively, and ignores unknown fields; the wire contract
// (ref 02 §2, abi-profile.schema.json: additionalProperties false) allows none
// of that. strictScan walks the raw JSON once, before any struct decoding, and
// rejects: unknown keys, keys that differ from the contract's exact spelling,
// duplicate keys, missing required keys, a non-object where an object is
// required, and anything after the first value.

// shape describes one JSON value of the profile artifact.
type shape struct {
	keys     map[string]*shape // exact allowed keys of an object; nil value = leaf
	required []string          // keys that must be present
	elem     *shape            // element shape, for arrays
	free     bool              // an object whose key names are data (effect_resolution.map); duplicates still rejected
}

// schemaRefShape is the shape of a request/response schema reference: exactly id and digest, both required.
var schemaRefShape = &shape{keys: map[string]*shape{"id": nil, "digest": nil}, required: []string{"id", "digest"}}

// profileShape is the exact wire shape of an AbiProfile artifact (abi-profile.schema.json, additionalProperties false).
var profileShape = &shape{
	keys: map[string]*shape{
		"kind": nil, "profile_version": nil, "profile_id": nil, "required_host_capabilities": nil,
		"limits": {keys: map[string]*shape{"max_request_bytes": nil, "max_response_bytes": nil, "timeout_ms": nil}},
		"operations": {elem: &shape{
			keys: map[string]*shape{
				"name": nil, "host_boundary": nil, "effect": nil,
				"effect_resolution": {
					keys:     map[string]*shape{"source": nil, "field": nil, "map": {free: true}, "default": nil},
					required: []string{"source", "field", "map", "default"},
				},
				"request_schema": schemaRefShape, "response_schema": schemaRefShape,
			},
			required: []string{"name", "host_boundary", "effect", "request_schema", "response_schema"},
		}},
	},
	required: []string{"kind", "profile_version", "profile_id", "operations", "required_host_capabilities"},
}

// strictScan validates raw against the shape and rejects any trailing data.
func strictScan(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := scanValue(dec, profileShape, "$"); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("unexpected data after the profile object")
	}
	return nil
}

// scanValue consumes one JSON value from dec against sh, rejecting what the shape does not allow.
func scanValue(dec *json.Decoder, sh *shape, path string) error {
	if sh == nil { // a leaf: any single JSON value, validated later by the typed decode
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
			if err := scanValue(dec, sh.elem, fmt.Sprintf("%s[%d]", path, i)); err != nil {
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
			return fmt.Errorf("%s: duplicate key %q", path, key)
		}
		seen[key] = true
		var child *shape
		if !sh.free {
			c, ok := sh.keys[key]
			if !ok {
				return fmt.Errorf("%s: unknown or differently spelled key %q", path, key)
			}
			child = c
		}
		if err := scanValue(dec, child, path+"."+key); err != nil {
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

// checkStrictLimits requires every declared limit to be a positive integer (the schema's minimum is 1);
// a zero or negative limit must not pass as "unset".
func checkStrictLimits(raw []byte) error {
	var doc struct {
		Limits map[string]int `json:"limits"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("limits: %v", err)
	}
	for k, v := range doc.Limits {
		if v < 1 {
			return fmt.Errorf("limits.%s must be a positive integer, got %d", k, v)
		}
	}
	return nil
}
