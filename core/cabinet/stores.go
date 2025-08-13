package cabinet

import "sync"

// In-memory stores (PoC)

type WorkflowStore interface {
    Get(id string) (*Workflow, bool)
    Put(w *Workflow) (prev *Workflow, existed bool)
    Exists(id string) bool
}

type ModuleStore interface {
    Get(id string) (*ModuleDescriptor, bool)
    Put(m *ModuleDescriptor) (prev *ModuleDescriptor, existed bool)
    Exists(id string) bool
}

type SchemaStore interface {
    Get(id string) (*Schema, bool)
    Put(s *Schema) (prev *Schema, existed bool)
    Exists(id string) bool
}

type memWorkflowStore struct {
    mu sync.RWMutex
    m  map[string]*Workflow
}

func NewMemWorkflowStore() WorkflowStore {
    return &memWorkflowStore{m: map[string]*Workflow{}}
}
func (s *memWorkflowStore) Get(id string) (*Workflow, bool) {
    s.mu.RLock(); defer s.mu.RUnlock()
    v, ok := s.m[id]; return v, ok
}
func (s *memWorkflowStore) Put(w *Workflow) (*Workflow, bool) {
    s.mu.Lock(); defer s.mu.Unlock()
    prev, existed := s.m[w.ID()]
    s.m[w.ID()] = w
    return prev, existed
}
func (s *memWorkflowStore) Exists(id string) bool {
    s.mu.RLock(); defer s.mu.RUnlock()
    _, ok := s.m[id]; return ok
}

type memModuleStore struct {
    mu sync.RWMutex
    m  map[string]*ModuleDescriptor
}

func NewMemModuleStore() ModuleStore {
    return &memModuleStore{m: map[string]*ModuleDescriptor{}}
}
func (s *memModuleStore) Get(id string) (*ModuleDescriptor, bool) {
    s.mu.RLock(); defer s.mu.RUnlock()
    v, ok := s.m[id]; return v, ok
}
func (s *memModuleStore) Put(m *ModuleDescriptor) (*ModuleDescriptor, bool) {
    s.mu.Lock(); defer s.mu.Unlock()
    prev, existed := s.m[m.ID()]
    s.m[m.ID()] = m
    return prev, existed
}
func (s *memModuleStore) Exists(id string) bool {
    s.mu.RLock(); defer s.mu.RUnlock()
    _, ok := s.m[id]; return ok
}

type memSchemaStore struct {
    mu sync.RWMutex
    m  map[string]*Schema
}

func NewMemSchemaStore() SchemaStore {
    return &memSchemaStore{m: map[string]*Schema{}}
}
func (s *memSchemaStore) Get(id string) (*Schema, bool) {
    s.mu.RLock(); defer s.mu.RUnlock()
    v, ok := s.m[id]; return v, ok
}
func (s *memSchemaStore) Put(sc *Schema) (*Schema, bool) {
    s.mu.Lock(); defer s.mu.Unlock()
    prev, existed := s.m[sc.ID()]
    s.m[sc.ID()] = sc
    return prev, existed
}
func (s *memSchemaStore) Exists(id string) bool {
    s.mu.RLock(); defer s.mu.RUnlock()
    _, ok := s.m[id]; return ok
}
