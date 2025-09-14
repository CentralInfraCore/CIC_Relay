package cabinet

// NewWorkflow constructs a Workflow by calling the single central entry point for validation and checksum calculation.
func NewWorkflow(meta BaseBlock, steps []string) (*Workflow, error) {
	// Call the single central entry point to validate and compute the checksum.
	sum, err := validateAndChecksum(&meta, KindWf, steps)
	if err != nil {
		return nil, err
	}
	meta.Checksum = sum
	meta.Valid = true

	return &Workflow{
		Meta:  meta,
		Steps: steps,
	}, nil
}

// ID returns the unique workflow identifier in the form "name@version".
func (w *Workflow) ID() string {
	return w.Meta.Name + "@" + w.Meta.Version
}

// GetData returns the workflow's steps for checksum purposes.
func (w *Workflow) GetData() any {
	return w.Steps
}

// Checksum returns the pre-computed checksum from the Meta block.
func (w *Workflow) Checksum() string {
	return w.Meta.Checksum
}
