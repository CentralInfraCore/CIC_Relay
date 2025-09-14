package cabinet

// NewSchema constructs a Schema by calling the single central entry point for validation and checksum calculation.
func NewSchema(meta BaseBlock, spec SchemaDef) (*Schema, error) {
	// Call the single central entry point to validate and compute the checksum.
	sum, err := validateAndChecksum(&meta, KindSpec, spec)
	if err != nil {
		return nil, err
	}
	meta.Checksum = sum
	meta.Valid = true

	return &Schema{
		Meta: meta,
		Spec: spec,
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

// Checksum returns the pre-computed checksum from the Meta block.
// It does not recalculate the hash, adhering to the "No shortcut" rule.
func (s *Schema) Checksum() string {
	return s.Meta.Checksum
}
