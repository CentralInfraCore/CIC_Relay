package cabinet

import "context"

// Cabinet defines the central API for schema/module/workflow management.
type Cabinet interface {
	PutSchema(s *Schema) error
	GetSchema(id string) (*Schema, error)

	PutModule(m *ModuleDescriptor) error
	GetModule(id string) (*ModuleDescriptor, error)

	PutWorkflow(w *Workflow) error
	GetWorkflow(id string) (*Workflow, error)

	// Set executes a workflow based on the payload.
	Set(ctx context.Context, in *SetPayload) (*SetResponse, error)
}

type cabinetService struct {
	schemas   SchemaStore
	modules   ModuleStore
	workflows WorkflowStore
}

func NewCabinetService(schemas SchemaStore, modules ModuleStore, workflows WorkflowStore) Cabinet {
	return &cabinetService{schemas: schemas, modules: modules, workflows: workflows}
}

// Set is the service-level implementation. The actual workflow execution is not yet implemented.
func (c *cabinetService) Set(ctx context.Context, in *SetPayload) (*SetResponse, error) {
	// TODO: Implement workflow execution logic here.
	return &SetResponse{
		Results:  map[string]any{},
		Warnings: nil,
	}, nil
}

func (c *cabinetService) PutSchema(s *Schema) error {
	if err := s.Validate(); err != nil {
		return err
	}
	c.schemas.Put(s)
	return nil
}

func (c *cabinetService) GetSchema(id string) (*Schema, error) {
	if s, ok := c.schemas.Get(id); ok {
		return s, nil
	}
	return nil, ErrNotFound
}

func (c *cabinetService) PutModule(m *ModuleDescriptor) error {
	if err := m.Validate(); err != nil {
		return err
	}
	c.modules.Put(m)
	return nil
}

func (c *cabinetService) GetModule(id string) (*ModuleDescriptor, error) {
	if m, ok := c.modules.Get(id); ok {
		return m, nil
	}
	return nil, ErrNotFound
}

func (c *cabinetService) PutWorkflow(w *Workflow) error {
	if err := w.Validate(); err != nil {
		return err
	}
	c.workflows.Put(w)
	return nil
}

func (c *cabinetService) GetWorkflow(id string) (*Workflow, error) {
	if w, ok := c.workflows.Get(id); ok {
		return w, nil
	}
	return nil, ErrNotFound
}
