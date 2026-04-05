package obs

import (
	"fmt"
	"strings"
	"sync"
)

const requiredPrefix = "relay."

// MetricInstrument describes a named metric that a component may emit.
type MetricInstrument struct {
	Name        string
	Description string
}

// Registry holds the set of declared MetricInstruments.
// All instrument names must start with the "relay." prefix.
type Registry struct {
	mu          sync.RWMutex
	instruments map[string]MetricInstrument
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{instruments: make(map[string]MetricInstrument)}
}

// Register adds inst to the registry.
// Returns an error if the name does not carry the required "relay." prefix
// or if a different instrument with the same name is already registered.
func (r *Registry) Register(inst MetricInstrument) error {
	if !strings.HasPrefix(inst.Name, requiredPrefix) {
		return fmt.Errorf("obs: instrument name %q must start with %q", inst.Name, requiredPrefix)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.instruments[inst.Name]; ok && existing != inst {
		return fmt.Errorf("obs: instrument %q already registered with different description", inst.Name)
	}
	r.instruments[inst.Name] = inst
	return nil
}

// Get returns the instrument registered under name, if any.
func (r *Registry) Get(name string) (MetricInstrument, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	inst, ok := r.instruments[name]
	return inst, ok
}

// Names returns all registered instrument names in undefined order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.instruments))
	for k := range r.instruments {
		names = append(names, k)
	}
	return names
}
