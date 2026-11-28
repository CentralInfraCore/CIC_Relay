// Package strictjson reads JSON that a producer and a consumer must read identically (O-1 INT-02).
//
// encoding/json alone accepts duplicate object keys (the last one wins), matches field names case-insensitively and,
// without DisallowUnknownFields, ignores unknown keys. For a signed or digest-bound wire object that is a hole: the
// same bytes can mean two things. Scan walks the raw JSON once, before any struct decoding, against an exact Shape,
// and refuses: unknown or differently spelled keys, duplicate keys (also inside objects whose key names are data),
// missing required keys, an object or array where the shape wants the other, and anything after the first value.
//
// It replaces the two byte-identical-in-behaviour copies that the #233 (the module-release envelope and the module-sign
// authority bindings) and #234 (the ABI-profile artifact) changes carried separately.
package strictjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Shape describes one JSON value. A nil *Shape is a leaf: any single JSON value, typed later by the struct decode.
type Shape struct {
	// Keys are the exact allowed keys of an object, each with the shape of its value.
	Keys map[string]*Shape
	// Required lists the keys that must be present.
	Required []string
	// Elem is the shape of every element, for an array.
	Elem *Shape
	// Free marks an object whose key names are data (any key is allowed); duplicates are still refused.
	Free bool
}

// maxKeyShown bounds how much of an offending key an error message repeats.
const maxKeyShown = 64

// clip bounds an offending key before it is repeated in an error message.
func clip(s string) string {
	if len(s) > maxKeyShown {
		return s[:maxKeyShown] + "..."
	}
	return s
}

// Scan validates raw against sh and refuses trailing data.
func Scan(raw []byte, sh *Shape) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := scanValue(dec, sh, "$"); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("unexpected data after the JSON value")
	}
	return nil
}

// scanValue consumes one JSON value from dec against sh; path names the position in error messages.
func scanValue(dec *json.Decoder, sh *Shape, path string) error {
	if sh == nil { // a leaf
		var skip json.RawMessage
		return dec.Decode(&skip)
	}
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	delim, _ := tok.(json.Delim)
	if sh.Elem != nil {
		if delim != '[' {
			return fmt.Errorf("%s: an array is required", path)
		}
		for i := 0; dec.More(); i++ {
			if err := scanValue(dec, sh.Elem, fmt.Sprintf("%s[%d]", path, i)); err != nil {
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
			return fmt.Errorf("%s: duplicate key %q", path, clip(key))
		}
		seen[key] = true
		var child *Shape
		if !sh.Free {
			c, ok := sh.Keys[key]
			if !ok {
				return fmt.Errorf("%s: unknown or differently spelled key %q", path, clip(key))
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
	for _, r := range sh.Required {
		if !seen[r] {
			return fmt.Errorf("%s: required key %q is missing", path, r)
		}
	}
	return nil
}
