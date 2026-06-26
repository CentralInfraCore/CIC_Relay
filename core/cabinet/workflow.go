package cabinet

import (
	"context"
	"fmt"
)

// NewWorkflow constructs a ManagedEntity-conformant Workflow with validated checksum.
func NewWorkflow(ctx context.Context, identity WorkflowIdentity, config WorkflowConfig, signing WorkflowSigning) (*Workflow, error) {
	if len(config.Steps) == 0 {
		return nil, fmt.Errorf("workflow must have at least one step")
	}

	sum, err := validateAndChecksumWorkflow(ctx, identity, config, signing)
	if err != nil {
		return nil, err
	}
	signing.Checksum = sum

	return &Workflow{
		Identity: identity,
		Config:   config,
		Signing:  signing,
		State:    WorkflowState{Valid: true},
	}, nil
}

// ID returns the unique workflow identifier in the form "name@version".
// This is used for lookups in the workflow store.
func (w *Workflow) ID() string {
	return w.Config.Name + "@" + w.Identity.Version
}

// GetData returns the workflow's config surface for checksum purposes.
func (w *Workflow) GetData() any {
	return w.Config
}

// Checksum returns the pre-computed checksum from the Signing block.
func (w *Workflow) Checksum() string {
	return w.Signing.Checksum
}
