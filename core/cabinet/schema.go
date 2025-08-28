package cabinet

// ID returns the unique schema identifier in the form "name@version".
func (s *Schema) ID() string {
    return s.Meta.Name + "@" + s.Meta.Version
}

// GetData returns the schema's specification for checksum purposes.
func (s *Schema) GetData() any {
    return s.Spec
}

// Validate runs common meta validation for the schema.
func (s *Schema) Validate() error {
    return validateMetaFunc(&s.Meta, s.GetData())
}

// Checksum újraszámolja a sémára vonatkozó hash-t a Meta és Spec mező alapján.
func (s *Schema) Checksum() (string, error) {
	return calculateChecksum(&s.Meta, s.Spec)
}
