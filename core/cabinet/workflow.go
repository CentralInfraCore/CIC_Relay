package cabinet

// ID returns the unique workflow identifier in the form "name@version".
func (w *Workflow) ID() string {
    return w.Meta.Name + "@" + w.Meta.Version
}

// GetData returns the workflow's steps for checksum purposes.
func (w *Workflow) GetData() any {
    return w.Steps
}

// Validate runs common meta validation for the workflow.
func (w *Workflow) Validate() error {
    return ValidateMeta(&w.Meta, w.GetData())
}
