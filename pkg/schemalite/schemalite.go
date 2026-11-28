// Package schemalite validates JSON payloads against a small, closed subset of JSON Schema (O-1 INT-07).
//
// The subset is deliberately closed and fails closed: a schema that uses any keyword outside it is REFUSED AT COMPILE
// TIME, never silently ignored, because an ignored keyword is a constraint the operator believes is enforced and is not.
// Supported: type (object, array, string, integer, number, boolean, null), properties, required, additionalProperties
// (boolean), items (one schema), enum, const, minLength, maxLength, minimum, maximum, minItems, maxItems, pattern (RE2);
// $schema, $id, title and description are annotations and have no effect. Everything else (for example $ref, oneOf,
// allOf, format, patternProperties) is unsupported and rejected.
//
// A payload is exactly one JSON value; trailing data is refused. Numbers are read as json.Number, so an integer is
// recognised exactly and no precision is lost on the way in.
package schemalite

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"unicode/utf8"
)

// Schema is a compiled schema.
type Schema struct {
	typ      string
	props    map[string]*Schema
	required []string
	noExtra  bool
	items    *Schema
	enum     []any
	hasConst bool
	constVal any
	minLen   *int
	maxLen   *int
	minimum  *float64
	maximum  *float64
	minItems *int
	maxItems *int
	pattern  *regexp.Regexp
}

// annotations are accepted and ignored.
var annotations = map[string]bool{"$schema": true, "$id": true, "title": true, "description": true}

// types is the closed set of supported type names.
var types = map[string]bool{"object": true, "array": true, "string": true, "integer": true, "number": true, "boolean": true, "null": true}

// decodeOne decodes exactly one JSON value, numbers as json.Number, and refuses trailing data.
func decodeOne(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return v, nil
}

// Compile compiles a schema document. Any unsupported keyword or malformed value is an error.
func Compile(raw []byte) (*Schema, error) {
	v, err := decodeOne(raw)
	if err != nil {
		return nil, fmt.Errorf("schema: %v", err)
	}
	return compile(v, "$")
}

// compile compiles one schema object; path names it in errors.
func compile(v any, path string) (*Schema, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema %s: a schema must be an object", path)
	}
	s := &Schema{}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		val := m[k]
		var err error
		switch k {
		case "type":
			t, ok := val.(string)
			if !ok || !types[t] {
				return nil, fmt.Errorf("schema %s: type must be one of object, array, string, integer, number, boolean, null", path)
			}
			s.typ = t
		case "properties":
			pm, ok := val.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("schema %s: properties must be an object", path)
			}
			s.props = map[string]*Schema{}
			for name, sub := range pm {
				if s.props[name], err = compile(sub, path+".properties."+name); err != nil {
					return nil, err
				}
			}
		case "required":
			arr, ok := val.([]any)
			if !ok {
				return nil, fmt.Errorf("schema %s: required must be an array of strings", path)
			}
			for _, r := range arr {
				name, ok := r.(string)
				if !ok {
					return nil, fmt.Errorf("schema %s: required must be an array of strings", path)
				}
				s.required = append(s.required, name)
			}
		case "additionalProperties":
			b, ok := val.(bool)
			if !ok {
				return nil, fmt.Errorf("schema %s: additionalProperties must be a boolean (a schema here is unsupported)", path)
			}
			s.noExtra = !b
		case "items":
			if s.items, err = compile(val, path+".items"); err != nil {
				return nil, err
			}
		case "enum":
			arr, ok := val.([]any)
			if !ok || len(arr) == 0 {
				return nil, fmt.Errorf("schema %s: enum must be a non-empty array", path)
			}
			for _, e := range arr {
				if !scalar(e) {
					return nil, fmt.Errorf("schema %s: enum values must be scalars", path)
				}
			}
			s.enum = arr
		case "const":
			if !scalar(val) {
				return nil, fmt.Errorf("schema %s: const must be a scalar", path)
			}
			s.hasConst, s.constVal = true, val
		case "minLength", "maxLength", "minItems", "maxItems":
			n, ok := nonNegativeInt(val)
			if !ok {
				return nil, fmt.Errorf("schema %s: %s must be a non-negative integer", path, k)
			}
			switch k {
			case "minLength":
				s.minLen = &n
			case "maxLength":
				s.maxLen = &n
			case "minItems":
				s.minItems = &n
			default:
				s.maxItems = &n
			}
		case "minimum", "maximum":
			f, ok := number(val)
			if !ok {
				return nil, fmt.Errorf("schema %s: %s must be a number", path, k)
			}
			if k == "minimum" {
				s.minimum = &f
			} else {
				s.maximum = &f
			}
		case "pattern":
			src, ok := val.(string)
			if !ok {
				return nil, fmt.Errorf("schema %s: pattern must be a string", path)
			}
			re, err := regexp.Compile(src)
			if err != nil {
				return nil, fmt.Errorf("schema %s: pattern: %v", path, err)
			}
			s.pattern = re
		default:
			if !annotations[k] {
				return nil, fmt.Errorf("schema %s: keyword %q is not supported (the subset is closed; it is refused rather than ignored)", path, k)
			}
		}
	}
	return s, nil
}

// scalar reports whether v is a JSON string, number, boolean or null.
func scalar(v any) bool {
	switch v.(type) {
	case nil, bool, string, json.Number:
		return true
	}
	return false
}

// number reads a JSON number as a finite float64.
func number(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil && !math.IsInf(f, 0)
}

// nonNegativeInt reads a JSON number as a non-negative int within int32.
func nonNegativeInt(v any) (int, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	if err != nil || i < 0 || i > math.MaxInt32 {
		return 0, false
	}
	return int(i), true
}

// isInteger reports whether a json.Number is an integer value (1 and 1.0 and 1e2 are; 1.5 is not).
func isInteger(n json.Number) bool {
	f, err := n.Float64()
	return err == nil && !math.IsInf(f, 0) && f == math.Trunc(f)
}

// Validate checks one JSON payload against the schema.
func (s *Schema) Validate(raw []byte) error {
	v, err := decodeOne(raw)
	if err != nil {
		return fmt.Errorf("payload: %v", err)
	}
	return s.check(v, "$")
}

// kindOf names the JSON kind of a decoded value (integer before number).
func kindOf(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		if isInteger(t) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	default:
		return "object"
	}
}

// check validates one decoded value against s; path names it in errors.
func (s *Schema) check(v any, path string) error {
	if s.typ != "" {
		k := kindOf(v)
		if k != s.typ && !(s.typ == "number" && k == "integer") {
			return fmt.Errorf("%s: want %s, got %s", path, s.typ, k)
		}
	}
	if len(s.enum) > 0 {
		found := false
		for _, e := range s.enum {
			if equal(e, v) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: not one of the enumerated values", path)
		}
	}
	if s.hasConst && !equal(s.constVal, v) {
		return fmt.Errorf("%s: is not the required constant", path)
	}
	switch t := v.(type) {
	case string:
		n := utf8.RuneCountInString(t)
		if s.minLen != nil && n < *s.minLen {
			return fmt.Errorf("%s: shorter than %d", path, *s.minLen)
		}
		if s.maxLen != nil && n > *s.maxLen {
			return fmt.Errorf("%s: longer than %d", path, *s.maxLen)
		}
		if s.pattern != nil && !s.pattern.MatchString(t) {
			return fmt.Errorf("%s: does not match the pattern", path)
		}
	case json.Number:
		f, _ := t.Float64()
		if s.minimum != nil && f < *s.minimum {
			return fmt.Errorf("%s: below the minimum %v", path, *s.minimum)
		}
		if s.maximum != nil && f > *s.maximum {
			return fmt.Errorf("%s: above the maximum %v", path, *s.maximum)
		}
	case []any:
		if s.minItems != nil && len(t) < *s.minItems {
			return fmt.Errorf("%s: fewer than %d items", path, *s.minItems)
		}
		if s.maxItems != nil && len(t) > *s.maxItems {
			return fmt.Errorf("%s: more than %d items", path, *s.maxItems)
		}
		if s.items != nil {
			for i, it := range t {
				if err := s.items.check(it, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case map[string]any:
		for _, r := range s.required {
			if _, ok := t[r]; !ok {
				return fmt.Errorf("%s: required property %q is missing", path, r)
			}
		}
		names := make([]string, 0, len(t))
		for k := range t {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			sub, known := s.props[k]
			if !known {
				if s.noExtra {
					return fmt.Errorf("%s: property %q is not allowed", path, k)
				}
				continue
			}
			if err := sub.check(t[k], path+"."+k); err != nil {
				return err
			}
		}
	}
	return nil
}

// equal compares two decoded scalar JSON values (numbers by value).
func equal(a, b any) bool {
	an, aok := a.(json.Number)
	bn, bok := b.(json.Number)
	if aok || bok {
		if !(aok && bok) {
			return false
		}
		af, _ := an.Float64()
		bf, _ := bn.Float64()
		return af == bf
	}
	return a == b
}
