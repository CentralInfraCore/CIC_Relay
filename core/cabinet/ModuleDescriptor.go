package cabinet

// GetData returns a stable copy of the module's key data fields, omitting volatile meta fields.
func (m *ModuleDescriptor) GetData() any {
    return struct {
        Type       string   `json:"type"`
        Accepts    []string `json:"accepts"`
        OutputKeys []string `json:"outputKeys"`
    }{
        Type:       m.Type,
        Accepts:    append([]string(nil), m.Accepts...),
        OutputKeys: append([]string(nil), m.OutputKeys...),
    }
}

// Validate runs common meta validation for the module.
func (m *ModuleDescriptor) Validate() error {
    return ValidateMeta(&m.Meta, m.GetData())
}
