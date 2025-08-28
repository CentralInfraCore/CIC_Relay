package cabinet

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

// -- ID() --

func TestModuleDescriptor_ID_Basic(t *testing.T) {
	m := ModuleDescriptor{Meta: BaseBlock{Name: "exec", Version: "1.0.0"}}
	if got, want := m.ID(), "exec@1.0.0"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

func TestModuleDescriptor_ID_EmptyParts(t *testing.T) {
	m := ModuleDescriptor{Meta: BaseBlock{Name: "", Version: ""}}
	if got, want := m.ID(), "@"; got != want {
		t.Fatalf("ID() = %q, want %q", got, want)
	}
}

// -- GetData() --

func TestModuleDescriptor_GetData_JSONShapeAndValues(t *testing.T) {
	m := ModuleDescriptor{
		Meta:       BaseBlock{Name: "mod", Version: "2.3.4"},
		Type:       "executor",
		Accepts:    []string{"schema.a", "schema.b"},
		OutputKeys: []string{"k1", "k2"},
	}

	got := m.GetData()

	// JSON-összehasonlítás a forma és érték miatt (típus-független).
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal(GetData) failed: %v", err)
	}

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
}

func TestModuleDescriptor_GetData_DefensiveCopies(t *testing.T) {
	orig := &ModuleDescriptor{
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

// -- Validate() --
// A Validate() teszteléséhez a korábban bevezetett teszt-varratot (validateMetaFunc) használjuk.

func TestModuleDescriptor_Validate_UsesValidateMetaAndPropagatesError(t *testing.T) {
	// Visszaállítás a teszt végén
	defer func(prev func(*BaseBlock, any) error) { validateMetaFunc = prev }(validateMetaFunc)

	var called bool
	var gotMeta *BaseBlock
	var gotData any
	mockErr := errors.New("boom")

	validateMetaFunc = func(meta *BaseBlock, data any) error {
		called = true
		gotMeta = meta
		gotData = data
		return mockErr
	}

	m := &ModuleDescriptor{
		Meta:       BaseBlock{Name: "mod", Version: "1.0.0"},
		Type:       "executor",
		Accepts:    []string{"S"},
		OutputKeys: []string{"O"},
	}

	err := m.Validate()
	if !called {
		t.Fatalf("validateMetaFunc was not called")
	}
	if !errors.Is(err, mockErr) {
		t.Fatalf("Validate() error = %v, want %v", err, mockErr)
	}
	if gotMeta != &m.Meta {
		t.Fatalf("validateMetaFunc meta ptr mismatch: got=%p want=%p", gotMeta, &m.Meta)
	}

	// Az átadott 'data' egyezzen a m.GetData() által adott struktúrával
	gotDataJSON, _ := json.Marshal(gotData)
	wantDataJSON, _ := json.Marshal(m.GetData())
	if !bytes.Equal(gotDataJSON, wantDataJSON) {
		t.Fatalf("validateMetaFunc data mismatch:\n got:  %s\n want: %s", string(gotDataJSON), string(wantDataJSON))
	}
}
