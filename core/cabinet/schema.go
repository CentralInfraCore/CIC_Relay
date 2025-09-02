package cabinet

// NewSchema constructs a Schema with validated metadata and canonical checksum.
// NOTE: If your Schema embeds BaseBlock instead of using a Meta field,
// replace assignments to Meta accordingly.
func NewSchema(meta BaseBlock, Spec SchemaDef) (*Schema, error) {
       // Validate meta + data together.
       if err := ValidateMeta(&meta, Spec); err != nil {
               return nil, err
       }

       // Compute canonical checksum and set it.
       sum, err := calculateChecksum(&meta, Spec)
       if err != nil {
               return nil, err
       }
       meta.Checksum = sum

       return &Schema{
               Meta: meta, // ← if you embed BaseBlock, replace with the embedded field.
               Spec: Spec,
       }, nil
}

// ID returns the unique schema identifier in the form "name@version".
func (s *Schema) ID() string {
    return s.Meta.Name + "@" + s.Meta.Version
}

// GetData returns the schema's specification for checksum purposes.
func (s *Schema) GetData() any {
    return s.Spec
}

// Validate checks meta+data invariants, verifies checksum if present,
// and delegates to ValidateMeta for schema-level rules.
func (s *Schema) Validate() error {
    return validateMetaFunc(&s.Meta, s.GetData())
}

// Checksum újraszámolja a sémára vonatkozó hash-t a Meta és Spec mező alapján.
func (s *Schema) Checksum() (string, error) {
	return calculateChecksum(&s.Meta, s.Spec)
}
