package canonicaljson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// ToJSON marshals a Go value into a canonical JSON string.
// It ensures that map keys are sorted alphabetically for deterministic output.
func ToJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonicalJSON(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonicalJSON(w io.Writer, v any) error {
	switch vv := v.(type) {
	case nil:
		_, err := w.Write([]byte("null"))
		return err
	case bool:
		_, err := w.Write([]byte(fmt.Sprintf("%t", vv)))
		return err
	case float64:
		// Use json.Marshal for correct float formatting (e.g., scientific notation)
		return writeJSONString(w, vv)
	case string:
		return writeJSONString(w, vv)
	case []any:
		return writeJSONArray(w, vv)
	case map[string]any:
		return writeJSONMap(w, vv)
	default:
		// For any other type, fall back to standard json.Marshal
		// This handles complex structs, other numeric types, etc.
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	}
}

func writeJSONString(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func writeJSONArray(w io.Writer, arr []any) error {
	if _, err := w.Write([]byte("[")); err != nil {
		return err
	}
	for i, item := range arr {
		if i > 0 {
			if _, err := w.Write([]byte(",")); err != nil {
				return err
			}
		}
		if err := writeCanonicalJSON(w, item); err != nil {
			return err
		}
	}
	_, err := w.Write([]byte("]"))
	return err
}

func writeJSONMap(w io.Writer, m map[string]any) error {
	if _, err := w.Write([]byte("{")); err != nil {
		return err
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for i, k := range keys {
		if i > 0 {
			if _, err := w.Write([]byte(",")); err != nil {
				return err
			}
		}
		if err := writeJSONString(w, k); err != nil {
			return err
		}
		if _, err := w.Write([]byte(":")); err != nil {
			return err
		}
		if err := writeCanonicalJSON(w, m[k]); err != nil {
			return err
		}
	}

	_, err := w.Write([]byte("}"))
	return err
}
