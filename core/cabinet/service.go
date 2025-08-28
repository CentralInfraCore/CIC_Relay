package cabinet

// Cabinet defines the central API for schema/module/workflow management.
type Cabinet interface {
    PutSchema(s *Schema) error
    GetSchema(id string) (*Schema, error)

    PutModule(m *ModuleDescriptor) error
    GetModule(id string) (*ModuleDescriptor, error)

    PutWorkflow(w *Workflow) error
    GetWorkflow(id string) (*Workflow, error)
}

type cabinetService struct {
    schemas  SchemaStore
    modules  ModuleStore
    workflows WorkflowStore
}

func NewCabinetService(schemas SchemaStore, modules ModuleStore, workflows WorkflowStore) Cabinet {
    return &cabinetService{schemas: schemas, modules: modules, workflows: workflows}
}

func (c *cabinetService) PutSchema(s *Schema) error {
    if err := s.Validate(); err != nil {
        return err
    }
//    if prev, exists := c.schemas.Put(s); exists && prev.Checksum != s.Checksum {
//        return ErrConflict
//    }
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
//    if prev, exists := c.modules.Put(m); exists && prev.Checksum != m.Checksum {
//        return ErrConflict
//    }
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
//    if prev, exists := c.workflows.Put(w); exists && prev.Checksum != w.Checksum {
//        return ErrConflict
//    }
    return nil
}

func (c *cabinetService) GetWorkflow(id string) (*Workflow, error) {
    if w, ok := c.workflows.Get(id); ok {
        return w, nil
    }
    return nil, ErrNotFound
}
