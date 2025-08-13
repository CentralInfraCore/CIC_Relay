package cabinet

// ID visszaadja a séma egyedi azonosítóját a név és verzió alapján.
func (s *Schema) ID() string {
	return s.Meta.Name + "@" + s.Meta.Version
}

// Checksum újraszámolja a sémára vonatkozó hash-t a Meta és Spec mező alapján.
func (s *Schema) Checksum() (string, error) {
	return calculateChecksum(&s.Meta, s.Spec)
}

// Validate ellenőrzi a kötelező mezők meglétét, újraszámolja a checksumot, ellenőrzi a signature-t és frissíti a Valid mezőt.
func (s *Schema) Validate() error {
	return ValidateMeta(&s.Meta, s.Spec)
}
