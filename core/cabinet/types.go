package cabinet

// ModuleID is the unique identifier of a module (string alias).
type ModuleID string

// BaseBlock contains common metadata and validation attributes of every object.
type BaseBlock struct {
	Name        string `json:"name" yaml:"name"`
	Version     string `json:"version" yaml:"version"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	CreatedBy   string `json:"createdBy,omitempty" yaml:"createdBy,omitempty"`
	Timestamp   string `json:"timestamp,omitempty" yaml:"timestamp,omitempty"`
	Checksum    string `json:"checksum,omitempty" yaml:"checksum,omitempty"`
	Signature   string `json:"signature,omitempty" yaml:"signature,omitempty"`
	Certificate string `json:"certificate,omitempty" yaml:"certificate,omitempty"`
	CRLRef      string `json:"crlRef,omitempty" yaml:"crlRef,omitempty"`
	Valid       bool   `json:"-" yaml:"-"` // Never read the Valid field; always set it at runtime.
}

// SchemaDef is the operational specification of a schema – state requirements, dependencies, next steps.
type SchemaDef struct {
	StateRequirement map[string]string `json:"stateRequirement,omitempty" yaml:"stateRequirement,omitempty"`
	Dependencies     []string          `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	NextHops         []string          `json:"nextHops,omitempty" yaml:"nextHops,omitempty"`
}

// Schema is the complete schema object, meta + operational specification.
type Schema struct {
	Meta BaseBlock `json:"metadata" yaml:"metadata"`
	Spec SchemaDef `json:"spec" yaml:"spec"`
}

// NativeFunctions is the storage for native, Go-implemented functions.
// The key is the function name, the value is the Go function itself.
type NativeFunctions map[string]interface{}

// ModuleDescriptor describes a module's characteristics – type, supported schemas, output keys.
type ModuleDescriptor struct {
	Meta       BaseBlock `json:"metadata" yaml:"metadata"`
	Type       string    `json:"type" yaml:"type"`
	Accepts    []string  `json:"accepts" yaml:"accepts"`
	OutputKeys []string  `json:"outputKeys,omitempty" yaml:"outputKeys,omitempty"`
	WasmCode   []byte    `json:"-" yaml:"-"` // Never serialize WasmCode; handle it in a separate file.

	// Runtime, non-persisted fields
	NativeImpl NativeFunctions `json:"-" yaml:"-"` // Storage for native Go implementations.
}

// Workflow is a list of execution steps in graph form – referencing schemas.
type Workflow struct {
	Meta  BaseBlock `json:"metadata" yaml:"metadata"`
	Steps []string  `json:"steps" yaml:"steps"`
}
