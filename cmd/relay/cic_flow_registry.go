package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"centralrelay/core/cabinet"
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
	git     map[string]gitGrant // per-module git reach, from the verified manifest; absent = denied
}

// gitGrant is a module's verified git reach, bound to the exact artifact it was derived from: the wasm
// digest loadModule verified against the loaded bytes, and the digest of the manifest document it read.
// A grant is dropped whenever the module ID is loaded again (revokeModuleCapability), so it can never
// outlive the artifact it was granted for.
type gitGrant struct {
	rules          []gitRule
	wasmDigest     string
	manifestDigest string
	// artifact is the checksum of the module artifact the cabinet stores for the load that produced this
	// grant. Published together with the grant (publishVerified); a grant without it is never usable.
	artifact string
}

// registerModuleCapability, if set, is called by the module-load path
// (`loadModule`) after a module's provenance verifies, to bind its cic-flow
// capability. `main` wires it to a registry's registerVerified; it stays nil in
// tests that do not exercise capability binding. A non-nil error rejects the
// module at load (fail-closed). It is set once at startup before any load.
var registerModuleCapability func(moduleID string, projectYAML []byte, artifact string) error

// revokeModuleCapability, if set, is called by loadModule as soon as a module's ID is known, before the
// module is examined, to drop whatever an earlier load of the same ID was granted. A grant belongs to
// the artifact it was verified for, not to the name; a reload starts from nothing and is re-granted
// only if it verifies. Set once at startup, like registerModuleCapability.
var revokeModuleCapability func(moduleID string)

// newCICFlowRegistry builds an empty registry. A nil doer defaults to the egress client
// (newEgressClient: no redirects, no proxy, timeouts, blocked address ranges); a nil
// resolve leaves every module's signer nil (sign inert) until the OCI-key provisioning
// path wires a real resolver.
func newCICFlowRegistry(resolve keyResolver, doer httpDoer) *cicFlowRegistry {
	if doer == nil {
		doer = newEgressClient(false)
	}
	return &cicFlowRegistry{resolve: resolve, doer: doer, deps: map[string]cicFlowDeps{}, git: map[string]gitGrant{}}
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
	return r.publishVerified(moduleID, projectYAML, "")
}

// publishVerified is registerVerified with the checksum of the artifact the cabinet is about to store for
// this module. The grant and the artifact it belongs to are published in ONE step under the registry lock,
// so there is no moment at which a grant exists without its artifact, or with an artifact that came from
// another load. A git grant published without an artifact ("") is never usable.
func (r *cicFlowRegistry) publishVerified(moduleID string, projectYAML []byte, artifact string) error {
	p, err := parseModuleProject(projectYAML)
	if err != nil {
		return err
	}
	usesFlow := len(p.cicFlowImports()) > 0
	usesGit := len(p.gitImports()) > 0 || len(p.CapabilityManifest.Git) > 0
	if !usesFlow && !usesGit {
		return nil // module uses neither cic-flow nor git — nothing to bind
	}
	if err := p.checkImportsWithinManifest(); err != nil {
		return err
	}
	var deps cicFlowDeps
	if usesFlow {
		deps, err = bindCICFlowDeps(p.CapabilityManifest, r.resolve, r.doer)
		if err != nil {
			return err
		}
	}
	sum := sha256.Sum256(projectYAML)
	r.mu.Lock()
	if usesFlow {
		r.deps[moduleID] = deps
	}
	if len(p.CapabilityManifest.Git) > 0 {
		r.git[moduleID] = gitGrant{
			rules:          append([]gitRule(nil), p.CapabilityManifest.Git...),
			wasmDigest:     p.WasmDigest,
			manifestDigest: hex.EncodeToString(sum[:]),
			artifact:       artifact,
		}
	}
	r.mu.Unlock()
	return nil
}

// revoke drops every capability bound to moduleID. loadModule calls it before it examines a module, so a
// reload of the same ID (a different artifact, an unsigned one, one whose verification fails) never keeps
// the previous artifact's grants.
func (r *cicFlowRegistry) revoke(moduleID string) {
	r.mu.Lock()
	delete(r.deps, moduleID)
	delete(r.git, moduleID)
	r.mu.Unlock()
}

// gitAuthRequest is the part of a git request the policy looks at. The module identity is NOT here: it
// comes from the runtime (the instance name), never from anything the guest sends.
type gitAuthRequest struct {
	RepoID  string `json:"repo_id"`
	Remote  string `json:"remote"`
	Refspec string `json:"refspec"`
	Ref     string `json:"ref"`
	URL     string `json:"url"`
	Branch  string `json:"branch"`
}

// authorizeGit decides whether the module (identified by the runtime-assigned instance name) may perform
// op on this request. No grant, an unlisted repository, operation, remote, ref or source: denied.
func (r *cicFlowRegistry) authorizeGit(instance cabinet.InstanceIdentity, op string, req gitAuthRequest) error {
	r.mu.RLock()
	g, ok := r.git[instance.Name]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("git: module has no verified git capability")
	}
	// The grant belongs to the verified artifact, not to the name: the calling instance must have been
	// created from exactly the artifact the grant was bound to. An instance started from an older artifact
	// (before a reload), from an unverified one, or from none at all carries a different or empty artifact
	// and is refused; a re-grant binds to the new artifact only.
	if g.artifact == "" || g.artifact != instance.Artifact {
		return fmt.Errorf("git: the running module is not the verified artifact this capability was granted to")
	}
	for _, rule := range g.rules {
		if rule.Repo != req.RepoID || !containsFold(rule.Ops, op) {
			continue
		}
		if gitRuleCovers(rule, op, req) {
			return nil
		}
	}
	return fmt.Errorf("git: %s on %q is not permitted for this module", op, clipForError(req.RepoID))
}

func gitRuleCovers(rule gitRule, op string, req gitAuthRequest) bool {
	switch op {
	case "status", "commit":
		return true
	case "fetch":
		return containsExact(rule.Remotes, req.Remote)
	case "checkout":
		return refAllowed(rule.Refs, req.Ref)
	case "clone":
		return containsExact(rule.Sources, req.URL) && refAllowed(rule.Refs, req.Branch)
	case "push":
		src, dst, hasDst := strings.Cut(req.Refspec, ":")
		if !containsExact(rule.Remotes, req.Remote) || !refAllowed(rule.Refs, src) {
			return false
		}
		return !hasDst || refAllowed(rule.Refs, dst)
	}
	return false
}

func containsExact(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// refAllowed: exact name, or "prefix/*" matching one or more path segments below prefix.
func refAllowed(patterns []string, ref string) bool {
	if ref == "" {
		return false
	}
	for _, pat := range patterns {
		if prefix, ok := strings.CutSuffix(pat, "/*"); ok {
			if strings.HasPrefix(ref, prefix+"/") && len(ref) > len(prefix)+1 {
				return true
			}
			continue
		}
		if pat == ref {
			return true
		}
	}
	return false
}

func clipForError(s string) string {
	if len(s) > 48 {
		return s[:48] + "…"
	}
	return s
}
