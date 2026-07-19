package main

import (
	"net/http"
	"sync"
)

// R3a live wiring (feature-011, r3-plan.md). The `cic-flow` host module is a
// single shared instance on the wazero runtime, but capabilities are per-module:
// each guest is instantiated `WithName(moduleID)` (core/cabinet/cicwasm.go), so a
// host function resolves the *calling* guest's deps by `mod.Name()` from this
// registry. A module with no entry is denied — fail-closed.
//
// The registry is populated at module load (bootstrap.go `loadModule`) ONLY after
// the module's provenance verified, so an untrusted module never gets a
// capability. (Today the gate is the existing dev cert-chain verify against the
// embedded CICDeveloperCA; R3b replaces it with CICSourceCA provenance +
// CICLicenseCA entitlement — see r3-plan.md.)
type cicFlowRegistry struct {
	mu      sync.RWMutex
	resolve keyResolver // may be nil → sign stays inert until the OCI key is provisioned
	doer    httpDoer    // egress transport shared by all modules
	deps    map[string]cicFlowDeps
}

// registerModuleCapability, if set, is called by the module-load path
// (`loadModule`) after a module's provenance verifies, to bind its cic-flow
// capability. `main` wires it to a registry's registerVerified; it stays nil in
// tests that do not exercise capability binding. A non-nil error rejects the
// module at load (fail-closed). It is set once at startup before any load.
var registerModuleCapability func(moduleID string, projectYAML []byte) error

// newCICFlowRegistry builds an empty registry. A nil doer defaults to
// http.DefaultClient; a nil resolve leaves every module's signer nil (sign inert)
// until the OCI-key provisioning path wires a real resolver.
func newCICFlowRegistry(resolve keyResolver, doer httpDoer) *cicFlowRegistry {
	if doer == nil {
		doer = http.DefaultClient
	}
	return &cicFlowRegistry{resolve: resolve, doer: doer, deps: map[string]cicFlowDeps{}}
}

// lookup returns the deps bound for moduleID, or false if the module is not
// registered (→ the host function denies the call, fail-closed).
func (r *cicFlowRegistry) lookup(moduleID string) (cicFlowDeps, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.deps[moduleID]
	return d, ok
}

// registerVerified parses the module's signed project.yaml, checks its cic-flow
// imports against the declared manifest, binds per-module deps, and registers
// them under moduleID. It must be called only after the module's provenance has
// verified (the caller's fail-closed gate).
//
//   - A module that does not import cic-flow registers nothing (returns nil).
//   - A module that imports cic-flow but under-declares its manifest is rejected
//     (error) — the load path surfaces this as a module-load failure.
func (r *cicFlowRegistry) registerVerified(moduleID string, projectYAML []byte) error {
	p, err := parseModuleProject(projectYAML)
	if err != nil {
		return err
	}
	if len(p.cicFlowImports()) == 0 {
		return nil // module doesn't use cic-flow — nothing to bind
	}
	if err := p.checkImportsWithinManifest(); err != nil {
		return err
	}
	deps, err := bindCICFlowDeps(p.CapabilityManifest, r.resolve, r.doer)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.deps[moduleID] = deps
	r.mu.Unlock()
	return nil
}
