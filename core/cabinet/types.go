package cabinet

// ModuleID a modul egyedi azonosítója (string alias).
type ModuleID string

// BaseBlock: minden objektum közös metaadatait és validációs jellemzőit tartalmazza.
type BaseBlock struct {
	Name        string
	Version     string
	Description string
	CreatedBy   string
	Timestamp   string
	Checksum    string // a (Meta + Spec) hash-e
	Signature   string // digitális aláírás
	Certificate string // aláíró tanúsítványa PEM formátumban (X.509 vagy nyers pubkey)
	CRLRef      string // visszavonási hivatkozás vagy OCSP URL
	Valid       bool   // validáció eredménye
}

// SchemaDef: egy séma működési specifikációja – állapotfeltételek, függőségek, következő lépések.
type SchemaDef struct {
	StateRequirement map[string]string // pl. "host.state": "ready"
	Dependencies     []string          // más sémákra való hivatkozás
	NextHops         []string          // következő végrehajtási ágak
}

// Schema: teljes séma objektum, meta + működési leírás.
type Schema struct {
	Meta BaseBlock
	Spec SchemaDef
}

// ModuleDescriptor: egy modul jellemzői – típusa, támogatott sémák, output kulcsok.
type ModuleDescriptor struct {
	Meta       BaseBlock
	Type       string   // pl. "executor", "validator"
	Accepts    []string // mely sémákat tud kezelni
	OutputKeys []string // milyen kulcsokat figyelhet meg vagy generálhat
	WasmCode   []byte   // A WASM modul bináris tartalma
}

// Workflow: végrehajtási lépések listája, gráf formában – sémákra hivatkozva.
type Workflow struct {
	Meta  BaseBlock
	Steps []string // séma nevekből álló végrehajtási sorrend
}
