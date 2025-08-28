package cabinet_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"centralrelay/core/cabinet"
)

// -- ID() --

func TestModuleDescriptor_ID_API_Basic(t *testing.T) {
	m := cabinet.ModuleDescriptor{Meta: cabinet.BaseBlock{Name: "exec", Version: "1.0.0"}}
	if got, want := m.ID(), "exec@1.0.0"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

func TestModuleDescriptor_ID_API_EmptyParts(t *testing.T) {
	m := cabinet.ModuleDescriptor{Meta: cabinet.BaseBlock{Name: "", Version: ""}}
	if got, want := m.ID(), "@"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

// -- GetData() --

func TestModuleDescriptor_GetData_API_JSONShapeAndValues(t *testing.T) {
	m := cabinet.ModuleDescriptor{
		Meta:       cabinet.BaseBlock{Name: "mod", Version: "2.3.4"},
		Type:       "executor",
		Accepts:    []string{"schema.a", "schema.b"},
		OutputKeys: []string{"k1", "k2"},
	}

	got := m.GetData()

	// JSON-re visszük és ellenőrizzük a kulcsok/értékek megfelelését.
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal(GetData) failed: %v", err)
	}

	// forma és érték elvárás
	wantStruct := struct {
		Type       string   `json:"type"`
		Accepts    []string `json:"accepts"`
		OutputKeys []string `json:"outputKeys"`
	}{
		Type:       "executor",
		Accepts:    []string{"schema.a", "schema.b"},
		OutputKeys: []string{"k1", "k2"},
	}
	wantJSON, err := json.Marshal(wantStruct)
	if err != nil {
		t.Fatalf("json.Marshal(wantStruct) failed: %v", err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("GetData JSON mismatch:\n got:  %s\n want: %s", string(gotJSON), string(wantJSON))
	}

	// plusz ellenőrzés: ne szivárogjon bele más mező (pl. Meta)
	var mapp map[string]any
	if err := json.Unmarshal(gotJSON, &mapp); err != nil {
		t.Fatalf("json.Unmarshal(GetData) failed: %v", err)
	}
	if len(mapp) != 3 {
		t.Fatalf("GetData should expose exactly 3 keys, got %d: %v", len(mapp), mapp)
	}
	if _, ok := mapp["type"]; !ok {
		t.Fatalf("missing key 'type'")
	}
	if _, ok := mapp["accepts"]; !ok {
		t.Fatalf("missing key 'accepts'")
	}
	if _, ok := mapp["outputKeys"]; !ok {
		t.Fatalf("missing key 'outputKeys'")
	}
	if _, ok := mapp["meta"]; ok {
		t.Fatalf("unexpected key 'meta' present")
	}
}

func TestModuleDescriptor_GetData_API_DefensiveCopies(t *testing.T) {
	orig := &cabinet.ModuleDescriptor{
		Type:       "validator",
		Accepts:    []string{"A", "B"},
		OutputKeys: []string{"X", "Y"},
	}

	data := orig.GetData()

	// baseline JSON
	baseJSON, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("json.Marshal(base) failed: %v", err)
	}

	// módosítjuk az eredeti szeleteket
	orig.Accepts[0] = "A*"
	orig.OutputKeys = append(orig.OutputKeys, "Z")

	// a GetData() által visszaadott érték NE változzon utólag
	afterJSON, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("json.Marshal(after) failed: %v", err)
	}
	if !bytes.Equal(baseJSON, afterJSON) {
		t.Fatalf("GetData result changed after mutating original slices:\n before: %s\n after:  %s", string(baseJSON), string(afterJSON))
	}
}
