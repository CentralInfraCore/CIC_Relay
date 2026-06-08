package cabinet

// ModuleID a modul egyedi azonosítója (string alias).
type ModuleID string

// BaseBlock: minden objektum közös metaadatait és validációs jellemzőit tartalmazza.
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
	Valid       bool   `json:"-" yaml:"-"` // A Valid mezőt soha ne olvassuk be, mindig futásidőben állítjuk be.
}

// SchemaIdentity: stabil azonosítás — kind, namespace, version.
type SchemaIdentity struct {
	Kind      string `json:"kind,omitempty" yaml:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Version   string `json:"version" yaml:"version"`
}

// SchemaConfig: desired-state surface — a séma által előírt konfiguráció és működési feltételek.
type SchemaConfig struct {
	Name             string            `json:"name" yaml:"name"`
	Description      string            `json:"description,omitempty" yaml:"description,omitempty"`
	CreatedBy        string            `json:"createdBy,omitempty" yaml:"createdBy,omitempty"`
	Timestamp        string            `json:"timestamp,omitempty" yaml:"timestamp,omitempty"`
	StateRequirement map[string]string `json:"stateRequirement,omitempty" yaml:"stateRequirement,omitempty"`
	Dependencies     []string          `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	NextHops         []string          `json:"nextHops,omitempty" yaml:"nextHops,omitempty"`
}

// SchemaSigning: kriptográfiai bizonyíték mezők — checksum, aláírás, tanúsítvány.
type SchemaSigning struct {
	Checksum    string `json:"checksum,omitempty" yaml:"checksum,omitempty"`
	Signature   string `json:"signature,omitempty" yaml:"signature,omitempty"`
	Certificate string `json:"certificate,omitempty" yaml:"certificate,omitempty"`
	CRLRef      string `json:"crlRef,omitempty" yaml:"crlRef,omitempty"`
}

// SchemaState: observed/runtime state — betöltéskor állítja be a rendszer, soha nem szerializálják.
type SchemaState struct {
	Valid bool `json:"-" yaml:"-"`
}

// Schema: ManagedEntity-konform séma objektum — Identity + Config + Signing + State.
type Schema struct {
	Identity SchemaIdentity `json:"identity" yaml:"identity"`
	Config   SchemaConfig   `json:"config" yaml:"config"`
	Signing  SchemaSigning  `json:"signing,omitempty" yaml:"signing,omitempty"`
	State    SchemaState    `json:"-" yaml:"-"`
}

// NativeFunctions a natív, Go-ban implementált függvények tárolója.
// A kulcs a függvény neve, az érték maga a Go függvény.
type NativeFunctions map[string]interface{}

// ModuleDescriptor: egy modul jellemzői – típusa, támogatott sémák, output kulcsok.
type ModuleDescriptor struct {
	Meta       BaseBlock `json:"metadata" yaml:"metadata"`
	Type       string    `json:"type" yaml:"type"`
	Accepts    []string  `json:"accepts" yaml:"accepts"`
	OutputKeys []string  `json:"outputKeys,omitempty" yaml:"outputKeys,omitempty"`
	WasmCode   []byte    `json:"-" yaml:"-"` // A WasmCode-ot soha nem szerializáljuk, külön fájlban kezeljük.

	// Futásidejű, nem-perzisztált mezők
	NativeImpl NativeFunctions `json:"-" yaml:"-"` // A natív Go implementációkat tartalmazó tároló.
}

// Workflow: végrehajtási lépések listája, gráf formában – sémákra hivatkozva.
type Workflow struct {
	Meta  BaseBlock `json:"metadata" yaml:"metadata"`
	Steps []string  `json:"steps" yaml:"steps"`
}
