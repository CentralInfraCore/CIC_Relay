package cabinet

// ID returns the unique module identifier in the form "name@version".
func (m *ModuleDescriptor) ID() string {
	return m.Meta.Name + "@" + m.Meta.Version
}

// GetData returns the data part of the module descriptor for checksum calculation.
// For WASM modules, the checksum is calculated over the binary code itself,
// not the descriptive spec fields.
func (m *ModuleDescriptor) GetData() any {
	// If WasmCode is present, it's the data we want to checksum.
	if len(m.WasmCode) > 0 {
		return m.WasmCode
	}

	// Fallback for non-WASM modules or modules without code.
	// The checksum covers the descriptive fields. To ensure immutability,
	// we return copies of the slices.
	acceptsCopy := make([]string, len(m.Accepts))
	copy(acceptsCopy, m.Accepts)

	outputKeysCopy := make([]string, len(m.OutputKeys))
	copy(outputKeysCopy, m.OutputKeys)

	return struct {
		Type       string   `json:"type"`
		Accepts    []string `json:"accepts"`
		OutputKeys []string `json:"outputKeys"`
	}{
		Type:       m.Type,
		Accepts:    acceptsCopy,
		OutputKeys: outputKeysCopy,
	}
}

// Validate runs common meta validation for the module descriptor.
// It implicitly validates the checksum of the WasmCode if it's present.
func (m *ModuleDescriptor) Validate() error {
	return validateMetaFunc(&m.Meta, m.GetData())
}
