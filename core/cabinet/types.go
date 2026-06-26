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

// SchemaIdentity provides stable identification – kind, namespace, version.
type SchemaIdentity struct {
	Kind      string `json:"kind,omitempty" yaml:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Version   string `json:"version" yaml:"version"`
}

// SchemaConfig is the desired-state surface – schema-defined configuration and operational constraints.
type SchemaConfig struct {
	Name             string            `json:"name" yaml:"name"`
	Description      string            `json:"description,omitempty" yaml:"description,omitempty"`
	CreatedBy        string            `json:"createdBy,omitempty" yaml:"createdBy,omitempty"`
	Timestamp        string            `json:"timestamp,omitempty" yaml:"timestamp,omitempty"`
	StateRequirement map[string]string `json:"stateRequirement,omitempty" yaml:"stateRequirement,omitempty"`
	Dependencies     []string          `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	NextHops         []string          `json:"nextHops,omitempty" yaml:"nextHops,omitempty"`
}

// SchemaSigning holds cryptographic evidence fields – checksum, signature, certificate.
type SchemaSigning struct {
	Checksum    string `json:"checksum,omitempty" yaml:"checksum,omitempty"`
	Signature   string `json:"signature,omitempty" yaml:"signature,omitempty"`
	Certificate string `json:"certificate,omitempty" yaml:"certificate,omitempty"`
	CRLRef      string `json:"crlRef,omitempty" yaml:"crlRef,omitempty"`
}

// SchemaState is the observed/runtime state – set by the system on load, never serialized.
type SchemaState struct {
	Valid bool `json:"-" yaml:"-"`
}

// Schema is a ManagedEntity-conforming schema object – Identity + Config + Signing + State.
type Schema struct {
	Identity SchemaIdentity `json:"identity" yaml:"identity"`
	Config   SchemaConfig   `json:"config" yaml:"config"`
	Signing  SchemaSigning  `json:"signing,omitempty" yaml:"signing,omitempty"`
	State    SchemaState    `json:"-" yaml:"-"`
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

// WorkflowIdentity provides stable identification – kind, namespace, version.
type WorkflowIdentity struct {
	Kind      string `json:"kind,omitempty" yaml:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Version   string `json:"version" yaml:"version"`
}

// WorkflowConfig is the desired-state surface – workflow execution steps and metadata.
type WorkflowConfig struct {
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	CreatedBy   string   `json:"createdBy,omitempty" yaml:"createdBy,omitempty"`
	Timestamp   string   `json:"timestamp,omitempty" yaml:"timestamp,omitempty"`
	Steps       []string `json:"steps" yaml:"steps"`
}

// WorkflowSigning holds cryptographic evidence fields.
type WorkflowSigning struct {
	Checksum    string `json:"checksum,omitempty" yaml:"checksum,omitempty"`
	Signature   string `json:"signature,omitempty" yaml:"signature,omitempty"`
	Certificate string `json:"certificate,omitempty" yaml:"certificate,omitempty"`
	CRLRef      string `json:"crlRef,omitempty" yaml:"crlRef,omitempty"`
}

// WorkflowState is the observed/runtime state – set by the system on load, never serialized.
type WorkflowState struct {
	Valid bool `json:"-" yaml:"-"`
}

// Workflow is a ManagedEntity-conforming workflow object – Identity + Config + Signing + State.
type Workflow struct {
	Identity WorkflowIdentity `json:"identity" yaml:"identity"`
	Config   WorkflowConfig   `json:"config" yaml:"config"`
	Signing  WorkflowSigning  `json:"signing,omitempty" yaml:"signing,omitempty"`
	State    WorkflowState    `json:"-" yaml:"-"`
}
