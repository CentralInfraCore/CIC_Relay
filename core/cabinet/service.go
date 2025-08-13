package cabinet

import (
    "context"
    "fmt"
    "sync"
)

type Cabinet interface {
    PutSchema(ctx context.Context, s *Schema) (*Schema, error)
    PutModule(ctx context.Context, m *ModuleDescriptor) (*ModuleDescriptor, error)
    PutWorkflow(ctx context.Context, w *Workflow) (*Workflow, error)

    GetSchema(ctx context.Context, id string) (*Schema, error)
    GetModule(ctx context.Context, id string) (*ModuleDescriptor, error)
    GetWorkflow(ctx context.Context, id string) (*Workflow, error)
}

type cabinetSvc struct {
    wf WorkflowStore
    md ModuleStore
    sc SchemaStore
    mu sync.RWMutex
}

func NewCabinet(wf WorkflowStore, md ModuleStore, sc SchemaStore) Cabinet {
    return &cabinetSvc{wf: wf, md: md, sc: sc}
}

func (c *cabinetSvc) PutSchema(ctx context.Context, s *Schema) (*Schema, error) {
    if err := s.Validate(); err != nil {
        return nil, err
    }
    if prev, existed := c.sc.Put(s); existed {
        if prev.Meta.Checksum != s.Meta.Checksum {
            return nil, fmt.Errorf("%w: schema %s version digest conflict", ErrConflict, s.ID())
        }
        return prev, nil // idempotens
    }
    return s, nil
}

func (c *cabinetSvc) PutModule(ctx context.Context, m *ModuleDescriptor) (*ModuleDescriptor, error) {
    if err := m.Validate(); err != nil {
        return nil, err
    }
    if prev, existed := c.md.Put(m); existed {
        if prev.Meta.Checksum != m.Meta.Checksum {
            return nil, fmt.Errorf("%w: module %s version digest conflict", ErrConflict, m.ID())
        }
        return prev, nil
    }
    return m, nil
}

func (c *cabinetSvc) PutWorkflow(ctx context.Context, w *Workflow) (*Workflow, error) {
    if err := w.Validate(); err != nil {
        return nil, err
    }
    if prev, existed := c.wf.Put(w); existed {
        if prev.Meta.Checksum != w.Meta.Checksum {
            return nil, fmt.Errorf("%w: workflow %s version digest conflict", ErrConflict, w.ID())
        }
        return prev, nil
    }
    return w, nil
}

func (c *cabinetSvc) GetSchema(ctx context.Context, id string) (*Schema, error) {
    if v, ok := c.sc.Get(id); ok { return v, nil }
    return nil, ErrNotFound
}
func (c *cabinetSvc) GetModule(ctx context.Context, id string) (*ModuleDescriptor, error) {
    if v, ok := c.md.Get(id); ok { return v, nil }
    return nil, ErrNotFound
}
func (c *cabinetSvc) GetWorkflow(ctx context.Context, id string) (*Workflow, error) {
    if v, ok := c.wf.Get(id); ok { return v, nil }
    return nil, ErrNotFound
}
