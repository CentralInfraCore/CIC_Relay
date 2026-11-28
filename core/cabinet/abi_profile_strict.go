package cabinet

import (
	"encoding/json"
	"fmt"

	"centralrelay/pkg/strictjson"
)

// This file is the strict wire reader of the ABI-profile artifact (review F4 of #234). The scanner itself is the
// shared pkg/strictjson (O-1 INT-02): encoding/json alone accepts duplicate object keys, matches field names
// case-insensitively and ignores unknown fields, none of which the wire contract (ref 02 §2,
// abi-profile.schema.json: additionalProperties false) allows. Only the exact shape of the artifact lives here.

// schemaRefShape is the shape of a request/response schema reference: exactly id and digest, both required.
var schemaRefShape = &strictjson.Shape{Keys: map[string]*strictjson.Shape{"id": nil, "digest": nil}, Required: []string{"id", "digest"}}

// profileShape is the exact wire shape of an AbiProfile artifact (abi-profile.schema.json, additionalProperties false).
var profileShape = &strictjson.Shape{
	Keys: map[string]*strictjson.Shape{
		"kind": nil, "profile_version": nil, "profile_id": nil, "required_host_capabilities": nil,
		"limits": {Keys: map[string]*strictjson.Shape{"max_request_bytes": nil, "max_response_bytes": nil, "timeout_ms": nil}},
		"operations": {Elem: &strictjson.Shape{
			Keys: map[string]*strictjson.Shape{
				"name": nil, "host_boundary": nil, "effect": nil,
				"effect_resolution": {
					Keys:     map[string]*strictjson.Shape{"source": nil, "field": nil, "map": {Free: true}, "default": nil},
					Required: []string{"source", "field", "map", "default"},
				},
				"request_schema": schemaRefShape, "response_schema": schemaRefShape,
			},
			Required: []string{"name", "host_boundary", "effect", "request_schema", "response_schema"},
		}},
	},
	Required: []string{"kind", "profile_version", "profile_id", "operations", "required_host_capabilities"},
}

// strictScan validates raw against the profile's exact wire shape and rejects any trailing data.
func strictScan(raw []byte) error { return strictjson.Scan(raw, profileShape) }

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
