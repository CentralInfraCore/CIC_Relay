package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"centralrelay/core/cabinet"
	nexusgit "centralrelay/core/nexus/git"
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
	// grant. Published together with the grant; a grant without it is never usable.
	artifact string
	// active is false while the load that produced the grant has not yet stored its artifact (a staged
	// grant) and true once it has (commitGrant). Only an active grant authorises anything, so nothing is
	// ever authorised by a grant whose module is not in the cabinet.
	active bool
}

// registerModuleCapability, if set, is called by the module-load path
// (`loadModule`) after a module's provenance verifies, to bind its cic-flow
// capability. `main` wires it to a registry's registerVerified; it stays nil in
// tests that do not exercise capability binding. A non-nil error rejects the
// module at load (fail-closed). It is set once at startup before any load.
var registerModuleCapability func(moduleID string, projectYAML []byte, artifact string) error

// commitModuleCapability, if set, is called by loadModule once the module's artifact is stored in the
// cabinet, to activate the grant staged for it. Set once at startup.
var commitModuleCapability func(moduleID, artifact string)

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
	return r.publish(moduleID, projectYAML, artifact, true)
}

// stageVerified is publishVerified for the loader: the grant is recorded with its artifact but is NOT yet
// active; commitGrant activates it once the module has been stored. Between the two nothing is authorised
// by it, and an instance created in that window carries no binding, so it stays unauthorised for good.
func (r *cicFlowRegistry) stageVerified(moduleID string, projectYAML []byte, artifact string) error {
	return r.publish(moduleID, projectYAML, artifact, false)
}

// commitGrant activates the staged grant of moduleID if, and only if, it is still the one staged for
// artifact. A stale or foreign commit does nothing.
func (r *cicFlowRegistry) commitGrant(moduleID, artifact string) {
	r.mu.Lock()
	if g, ok := r.git[moduleID]; ok && !g.active && g.artifact == artifact {
		g.active = true
		r.git[moduleID] = g
	}
	r.mu.Unlock()
}

// bindingFor is WasmManagerConfig.InstanceBinding: the digest of the manifest of the active grant for
// moduleID, or "" if there is none. An instance carries it from the moment it is created, so a later reload
// with a different manifest does not widen (or narrow) what that already-running instance may do.
func (r *cicFlowRegistry) bindingFor(moduleID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if g, ok := r.git[moduleID]; ok && g.active {
		return g.manifestDigest
	}
	return ""
}

func (r *cicFlowRegistry) publish(moduleID string, projectYAML []byte, artifact string, active bool) error {
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
			active:         active,
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
	if !g.active || g.artifact == "" || g.artifact != instance.Artifact {
		return fmt.Errorf("git: the running module is not the verified artifact this capability was granted to")
	}
	// ...and under the manifest that was in force when the instance was created. The same wasm loaded
	// again with another manifest is the same artifact; the instance still carries the manifest it started
	// under, and an instance that started under a different one is refused.
	if instance.Binding == "" || instance.Binding != g.manifestDigest {
		return fmt.Errorf("git: the running module started under a different capability manifest")
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
		// The refspec is reduced to the canonical form that ExecGitOps will execute (fully qualified on both
		// sides), and that is what the manifest is checked against: what is authorised is what is run.
		src, dst, err := nexusgit.CanonicalPushRefspec(req.Refspec)
		if err != nil {
			return false
		}
		return containsExact(rule.Remotes, req.Remote) &&
			refAllowedQualified(rule.Refs, src) && refAllowedQualified(rule.Refs, dst)
	}
	return false
}

// qualifyRef gives a short name the refs/heads/ form that a push treats it as.
func qualifyRef(ref string) string {
	if strings.HasPrefix(ref, "refs/") {
		return ref
	}
	return "refs/heads/" + ref
}

// refAllowedQualified is refAllowed with the manifest's patterns qualified the same way as the ref.
func refAllowedQualified(patterns []string, qualifiedRef string) bool {
	q := make([]string, 0, len(patterns))
	for _, p := range patterns {
		q = append(q, qualifyRef(p))
	}
	return refAllowed(q, qualifiedRef)
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
