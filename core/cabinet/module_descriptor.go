package cabinet

// NewModuleDescriptor constructs a ModuleDescriptor by calling the single central entry point for validation and checksum calculation.
func NewModuleDescriptor(meta BaseBlock, moduleType string, accepts []string, outputKeys []string, wasmCode []byte) (*ModuleDescriptor, error) {
	// Create a temporary descriptor to use its GetData() method, which contains the correct data selection logic.
	tempDescriptorForData := &ModuleDescriptor{
		Type:       moduleType,
		Accepts:    accepts,
		OutputKeys: outputKeys,
		WasmCode:   wasmCode,
	}
	data := tempDescriptorForData.GetData()

	// Call the single central entry point to validate and compute the checksum.
	sum, err := validateAndChecksum(&meta, KindModule, data)
	if err != nil {
		return nil, err
	}
	meta.Checksum = sum
	meta.Valid = true

	return &ModuleDescriptor{
		Meta:       meta,
		Type:       moduleType,
		Accepts:    accepts,
		OutputKeys: outputKeys,
		WasmCode:   wasmCode,
	}, nil
}

// ID returns the unique module identifier in the form "name@version".
func (m *ModuleDescriptor) ID() string {
	return m.Meta.Name + "@" + m.Meta.Version
}

// GetData returns the data part of the module descriptor for checksum calculation.
// For WASM modules, the checksum is calculated over the binary code itself,
// not the descriptive spec fields.
func (m *ModuleDescriptor) GetData() any {
	if len(m.WasmCode) > 0 {
		return m.WasmCode
	}

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

// Checksum returns the pre-computed checksum from the Meta block.
func (m *ModuleDescriptor) Checksum() string {
	return m.Meta.Checksum
}
