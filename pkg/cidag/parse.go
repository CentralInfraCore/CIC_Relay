package cidag

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"strconv"
)

// ParseStrict reads one JSON document into the generic model the package works on (nil, bool, int64, string,
// []any, map[string]any) and refuses what two correct readers could read differently: a duplicate member name,
// any number that is not an integer within int64, a constant (NaN, Infinity), trailing data and nesting deeper
// than MaxDepth. Every refusal is of class PARSE.
func ParseStrict(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fail(KindParse, "data after the document")
	}
	return v, nil
}

// parseValue reads one JSON value, refusing nesting deeper than MaxDepth.
func parseValue(dec *json.Decoder, depth int) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fail(KindParse, "%v", err)
	}
	switch t := tok.(type) {
	case json.Delim:
		if depth+1 > MaxDepth {
			return nil, fail(KindParse, "nesting deeper than %d", MaxDepth)
		}
		if t == '{' {
			return parseObject(dec, depth)
		}
		return parseArray(dec, depth)
	case json.Number:
		return parseInteger(string(t))
	}
	return tok, nil // a string, a bool or null: the only other tokens json.Decoder.Token returns
}

// parseObject reads the members of an object and refuses a duplicate member name.
func parseObject(dec *json.Decoder, depth int) (any, error) {
	m := map[string]any{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, fail(KindParse, "%v", err)
		}
		k := kt.(string) // json.Decoder.Token guarantees a string where an object expects a member name
		if _, dup := m[k]; dup {
			return nil, fail(KindParse, "duplicate member name %q", k)
		}
		v, err := parseValue(dec, depth+1)
		if err != nil {
			return nil, err
		}
		m[k] = v
	}
	if _, err := dec.Token(); err != nil {
		return nil, fail(KindParse, "%v", err)
	}
	return m, nil
}

// parseArray reads the elements of an array.
func parseArray(dec *json.Decoder, depth int) (any, error) {
	arr := []any{}
	for dec.More() {
		v, err := parseValue(dec, depth+1)
		if err != nil {
			return nil, err
		}
		arr = append(arr, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, fail(KindParse, "%v", err)
	}
	return arr, nil
}

// parseInteger reads a number that must be an integer literal within int64 (the minimum, whose magnitude has no int64 counterpart, is refused too).
func parseInteger(s string) (any, error) {
	if !intRe.MatchString(s) {
		return nil, fail(KindParse, "number %q is not an integer", s)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n == math.MinInt64 {
		return nil, fail(KindParse, "number %q is beyond int64", s)
	}
	return n, nil
}
