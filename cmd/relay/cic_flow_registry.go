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
	resolve keyResolver          // may be nil → sign stays inert until the OCI key is provisioned
	doer    httpDoer             // egress transport shared by all modules
	deps    map[string]flowGrant // per-module cic-flow deps, bound to the authenticated artifact; absent = denied
	git     map[string]gitGrant  // per-module git reach, from the verified manifest; absent = denied
	send    *sendConfig          // operator cic-flow.send configuration; nil → send modules are rejected at load
}

// flowGrant is a module's cic-flow deps, bound like a gitGrant to the artifact the cabinet stores and to the
// authenticated envelope identity (#162 V7, ENV-03). Only an active grant whose artifact and envelope digest are the
// calling instance's authorises anything.
type flowGrant struct {
	deps           cicFlowDeps
	artifact       string
	envelopeDigest string
	active         bool
}

// gitGrant is a module's verified git reach, bound to the exact artifact it was derived from: the wasm
// digest loadModule verified against the loaded bytes, and the digest of the manifest document it read.
// A grant is dropped whenever the module ID is loaded again (revokeModuleCapability), so it can never
// outlive the artifact it was granted for.
type gitGrant struct {
	rules          []gitRule
	wasmDigest     string
	manifestDigest string
	// envelopeDigest is the authenticated module identity (#162 ENV-03: sha256 of the CICMODENVv1 signed bytes, which
	// cover the raw and executed wasm, the full manifest and the signer). It is the instance binding: a grant
	// without it is never usable.
	envelopeDigest string
	// artifact is the checksum of the module artifact the cabinet stores for the load that produced this
	// grant. Published together with the grant; a grant without it is never usable.
	artifact string
	// active is false while the load that produced the grant has not yet stored its artifact (a staged
	// grant) and true once it has (commitGrant). Only an active grant authorises anything, so nothing is
	// ever authorised by a grant whose module is not in the cabinet.
	active bool
}

// registerModuleCapability, if set, is called by the module-load path
// (`loadModule`) once a module is authenticated (#162: envelope, signer authority,
// digests and provenance verified), to bind its capability under its authenticated
// identity (moduleID, envelopeDigest). `main` wires it to a registry's registerVerified; it stays nil in
// tests that do not exercise capability binding. A non-nil error rejects the
// module at load (fail-closed). It is set once at startup before any load.
var registerModuleCapability func(moduleID string, projectYAML []byte, artifact, envelopeDigest string) error

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
	return &cicFlowRegistry{resolve: resolve, doer: doer, deps: map[string]flowGrant{}, git: map[string]gitGrant{}}
}

// lookupFor returns the cic-flow deps of the calling instance, or false (→ the host function denies the call,
// fail-closed). The identity comes from the runtime, never from the guest: the grant must be active and bound to
// exactly the artifact the instance was compiled from and the envelope identity it started under (#162 V7), so a
// reused module ID with another digest inherits nothing.
func (r *cicFlowRegistry) lookupFor(instance cabinet.InstanceIdentity) (cicFlowDeps, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.deps[instance.Name]
	if !ok || !g.active || g.artifact == "" || g.artifact != instance.Artifact ||
		g.envelopeDigest == "" || g.envelopeDigest != instance.Binding {
		return cicFlowDeps{}, false
	}
	return g.deps, true
}

// stageVerified is the loader's registerModuleCapability: the grant is recorded with its artifact and its
// authenticated envelope identity but is NOT yet active; commitGrant activates it once the module has been stored.
// Between the two nothing is authorised by it, and an instance created in that window carries no binding, so it
// stays unauthorised for good. The grant and the artifact it belongs to are published in ONE step under the
// registry lock.
func (r *cicFlowRegistry) stageVerified(moduleID string, projectYAML []byte, artifact, envelopeDigest string) error {
	return r.publish(moduleID, projectYAML, artifact, envelopeDigest, false)
}

// commitGrant activates the staged grant of moduleID if, and only if, it is still the one staged for
// artifact. A stale or foreign commit does nothing.
func (r *cicFlowRegistry) commitGrant(moduleID, artifact string) {
	r.mu.Lock()
	if g, ok := r.git[moduleID]; ok && !g.active && g.artifact == artifact {
		g.active = true
		r.git[moduleID] = g
	}
	if g, ok := r.deps[moduleID]; ok && !g.active && g.artifact == artifact {
		g.active = true
		r.deps[moduleID] = g
	}
	r.mu.Unlock()
}

// bindingFor is WasmManagerConfig.InstanceBinding: the authenticated envelope digest of the active grant for
// moduleID (#162 ENV-03; it covers the manifest digest), or "" if there is none. An instance carries it from the
// moment it is created, so a later reload with a different manifest or artifact does not widen (or narrow) what
// that already-running instance may do.
func (r *cicFlowRegistry) bindingFor(moduleID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if g, ok := r.git[moduleID]; ok && g.active {
		return g.envelopeDigest
	}
	if g, ok := r.deps[moduleID]; ok && g.active {
		return g.envelopeDigest
	}
	return ""
}

// publish is the body of stageVerified. It parses the module's project.yaml and
// returns nil without binding anything when the module neither imports cic-flow nor touches git. Otherwise
// it rejects imports the manifest does not declare, binds the cic-flow dependencies (egress, signer) and
// records the git grant — its rules, the manifest digest and artifact, active or staged — under the lock.
func (r *cicFlowRegistry) publish(moduleID string, projectYAML []byte, artifact, envelopeDigest string, active bool) error {
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
	if p.cicFlowImports()["send"] {
		// A send module gets ONLY the send engine: no raw signer and no egress for sign/actuate, so a
		// wasm that imports them anyway (beyond what project.yaml declares) is refused at call time.
		eng, err := r.bindSend(p.CapabilityManifest, projectYAML)
		if err != nil {
			return err
		}
		deps = cicFlowDeps{send: eng}
	} else if usesFlow {
		deps, err = bindCICFlowDeps(p.CapabilityManifest, r.resolve, r.doer)
		if err != nil {
			return err
		}
	}
	sum := sha256.Sum256(projectYAML)
	r.mu.Lock()
	if usesFlow {
		r.deps[moduleID] = flowGrant{deps: deps, artifact: artifact, envelopeDigest: envelopeDigest, active: active}
	}
	if len(p.CapabilityManifest.Git) > 0 {
		r.git[moduleID] = gitGrant{
			rules:          append([]gitRule(nil), p.CapabilityManifest.Git...),
			wasmDigest:     p.WasmDigest,
			manifestDigest: hex.EncodeToString(sum[:]),
			envelopeDigest: envelopeDigest,
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
	// Push is withdrawn (gitPushDisabled): refused here whatever any grant says, so that a registry entry
	// that got past manifest validation by any route still authorises nothing.
	if op == "push" {
		return fmt.Errorf("git: %s", gitPushDisabled)
	}
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
	if g.envelopeDigest == "" || instance.Binding != g.envelopeDigest {
		return fmt.Errorf("git: the running module started under a different authenticated identity (envelope)")
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

// gitRuleCovers reports whether rule, already matched on repository and op, also covers the names the
// request carries: status and commit need nothing more, fetch needs a listed remote, checkout a permitted
// ref, clone a listed source URL and a permitted branch. Any other op is not covered.
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
	}
	return false
}

// containsExact reports whether list contains v, compared case-sensitively.
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

// clipForError truncates a guest-supplied value to 48 bytes for an error message, so a module cannot
// flood the logs through it.
func clipForError(s string) string {
	if len(s) > 48 {
		return s[:48] + "…"
	}
	return s
}
