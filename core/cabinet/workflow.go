package cabinet

import (
	"context"
	"fmt"
)

// NewWorkflow constructs a Workflow by calling the single central entry point for validation and checksum calculation.
func NewWorkflow(ctx context.Context, meta BaseBlock, steps []string) (*Workflow, error) {
	if len(steps) == 0 {
		return nil, fmt.Errorf("workflow must have at least one step")
	}

	// Call the single central entry point to validate and compute the checksum.
	sum, err := validateAndChecksum(ctx, &meta, KindWf, steps)
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
// This is used for lookups in the workflow store.
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
