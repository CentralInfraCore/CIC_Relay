package main

import "centralrelay/pkg/strictjson"

// The strict reading of the wire files the admission trusts (review F1/F4 of #233) is the shared pkg/strictjson
// (O-1 INT-02): the same reader the ABI-profile artifact uses, so a producer and a consumer read the same bytes the
// same way. Only the exact shapes of the two files live here.

// strictScan validates raw against one of the shapes below.
func strictScan(raw []byte, sh *strictjson.Shape) error { return strictjson.Scan(raw, sh) }

// envelopeJSONShape is the exact wire shape of a ModuleReleaseEnvelope (module-release-envelope.schema.json).
var envelopeJSONShape = &strictjson.Shape{
	Keys: map[string]*strictjson.Shape{
		"kind": nil, "envelope_version": nil, "module_id": nil, "wasm_sha256": nil, "executed_wasm_sha256": nil, "manifest_digest": nil,
		"signer": {Keys: map[string]*strictjson.Shape{"key_id": nil, "alg": nil}, Required: []string{"key_id", "alg"}},
	},
	// envelope_version is checked right after the scan so that a missing or unknown version stays ENVELOPE_VERSION.
	Required: []string{"kind", "module_id", "wasm_sha256", "executed_wasm_sha256", "manifest_digest", "signer"},
}

// authoritiesJSONShape is the exact wire shape of the module-sign authority bindings file.
var authoritiesJSONShape = &strictjson.Shape{
	Keys: map[string]*strictjson.Shape{
		"kind": nil, "version": nil,
		"authorities": {Elem: &strictjson.Shape{Keys: map[string]*strictjson.Shape{"key_id": nil, "roles": nil}, Required: []string{"key_id", "roles"}}},
	},
	Required: []string{"kind", "version", "authorities"},
}
