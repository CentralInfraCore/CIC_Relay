package cabinet

import "sync"

// WorkflowStore abstracts storage for Workflow entities.
type WorkflowStore interface {
	Get(id string) (*Workflow, bool)
	Put(w *Workflow) (prev *Workflow, existed bool)
	Exists(id string) bool
}

// ModuleStore abstracts storage for ModuleDescriptor entities.
type ModuleStore interface {
	Get(id string) (*ModuleDescriptor, bool)
	Put(m *ModuleDescriptor) (prev *ModuleDescriptor, existed bool)
	Exists(id string) bool
}

// SchemaStore abstracts storage for Schema entities.
type SchemaStore interface {
	Get(id string) (*Schema, bool)
	Put(s *Schema) (prev *Schema, existed bool)
	Exists(id string) bool
}

// --- In-memory implementations (PoC) ---

type memWorkflowStore struct {
	mu   sync.RWMutex
	byID map[string]*Workflow
}

func NewMemWorkflowStore() WorkflowStore {
	return &memWorkflowStore{byID: map[string]*Workflow{}}
}

func (s *memWorkflowStore) Get(id string) (*Workflow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.byID[id]
	return v, ok
}

func (s *memWorkflowStore) Put(w *Workflow) (*Workflow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.byID[w.ID()]
	s.byID[w.ID()] = w
	return prev, existed
}

func (s *memWorkflowStore) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.byID[id]
	return ok
}

// memModuleStore implements a content-addressable storage (CAS) for modules.
// It uses a two-level map: an index for name/version to checksum mapping,
// and a content store for checksum to module mapping.
type memModuleStore struct {
	mu         sync.RWMutex
	byID       map[string]string            // Index: id -> checksum
	byChecksum map[string]*ModuleDescriptor // CAS: checksum -> module
}

func NewMemModuleStore() ModuleStore {
	return &memModuleStore{
		byID:       map[string]string{},
		byChecksum: map[string]*ModuleDescriptor{},
	}
}

func (s *memModuleStore) Get(id string) (*ModuleDescriptor, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. Look up the checksum from the ID index.
	checksum, ok := s.byID[id]
	if !ok {
		return nil, false
	}

	// 2. Look up the module from the content-addressable store.
	module, ok := s.byChecksum[checksum]
	return module, ok
}

func (s *memModuleStore) Put(m *ModuleDescriptor) (*ModuleDescriptor, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := m.ID()
	checksum := m.Meta.Checksum

	// Check if a module with this ID already existed and find the previous version.
	var prev *ModuleDescriptor
	prevChecksum, existed := s.byID[id]
	if existed {
		prev = s.byChecksum[prevChecksum]
	}

	// Store the module in the content-addressable store.
	s.byChecksum[checksum] = m

	// Update the index to point to the new checksum for this ID.
	s.byID[id] = checksum

	return prev, existed
}

func (s *memModuleStore) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.byID[id]
	return ok
}

type memSchemaStore struct {
	mu   sync.RWMutex
	byID map[string]*Schema
}

func NewMemSchemaStore() SchemaStore {
	return &memSchemaStore{byID: map[string]*Schema{}}
}

func (s *memSchemaStore) Get(id string) (*Schema, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.byID[id]
	return v, ok
}

func (s *memSchemaStore) Put(sc *Schema) (*Schema, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.byID[sc.ID()]
	s.byID[sc.ID()] = sc
	return prev, existed
}

func (s *memSchemaStore) Exists(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.byID[id]
	return ok
}
