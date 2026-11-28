package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"

	"centralrelay/core/cabinet"
	"centralrelay/pkg/hostsign"
)

// INT-08/09: the production TraceV2Source. It answers the four questions the cabinet cannot answer itself, from what
// the relay actually knows:
//
//	executed identity   the admission record of the module (envelope digest, raw/executed wasm digests, manifest digest,
//	                    ABI profile pin, trust state), recorded by loadModule only for an authenticated module WITH an
//	                    admitted profile binding, and only valid for the exact artifact it was recorded for;
//	host interactions   the records the cic-flow.send engine wrote while the step ran, kept apart per step by the step scope;
//	authorization       the protected-operation refusal, with a digest of a decision record;
//	execution context   a digest of the context record (workflow, trust mode).
//
// What it does not have, and does not invent: a principal and a realization context (no authenticated principal reaches the
// cabinet in this build); the context record says so.

// admittedIdentity is the admission record of one module, bound to the artifact it was admitted for.
type admittedIdentity struct {
	checksum string // cabinet ModuleDescriptor.Checksum() at admission: a different artifact of the same id does not inherit it
	ident    cabinet.ExecutedIdentityV2
}

// identityRegistry holds the admission records, written by loadModule and read by the trace source.
type identityRegistry struct {
	mu sync.RWMutex
	m  map[string]admittedIdentity
}

// newIdentityRegistry returns an empty registry.
func newIdentityRegistry() *identityRegistry {
	return &identityRegistry{m: map[string]admittedIdentity{}}
}

// set records the admission of moduleID; a reload replaces it.
func (r *identityRegistry) set(moduleID, checksum string, ident cabinet.ExecutedIdentityV2) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[moduleID] = admittedIdentity{checksum: checksum, ident: ident}
}

// forget drops a module's record (a reload that is not admitted with a profile must not leave the earlier one).
func (r *identityRegistry) forget(moduleID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, moduleID)
}

// get returns the record only if it belongs to exactly this artifact.
func (r *identityRegistry) get(moduleID, checksum string) (cabinet.ExecutedIdentityV2, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.m[moduleID]
	if !ok || a.checksum != checksum {
		return cabinet.ExecutedIdentityV2{}, false
	}
	return a.ident, true
}

// moduleIdentities is the process-wide registry loadModule writes to.
var moduleIdentities = newIdentityRegistry()

// sha256Of is the sha256:<hex> form of the O-1 digests.
func sha256Of(b []byte) string { s := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(s[:]) }

// executedIdentityOf builds the admission record of a module from what loadModule verified.
func executedIdentityOf(rawWasmSHAHex string, mod *cabinet.ModuleDescriptor, manifest []byte, envelopeDigest string, b *cabinet.ModuleProfileBinding) cabinet.ExecutedIdentityV2 {
	env := envelopeDigest
	return cabinet.ExecutedIdentityV2{
		ArtifactDigest: "sha256:" + rawWasmSHAHex,
		ExecutedDigest: sha256Of(mod.WasmCode),
		ManifestDigest: sha256Of(manifest),
		EnvelopeDigest: &env,
		Profile:        cabinet.ProfileRefV2{ID: b.ProfileID, Digest: b.ProfileDigest},
		TrustState:     cabinet.TrustStateAuthenticated,
	}
}

// traceSource is the production cabinet.TraceV2Source (and StepScoper).
type traceSource struct {
	identities *identityRegistry
	trustMode  string
	protected  map[string]bool

	mu      sync.Mutex
	archive map[string][]hostsign.Record // interaction-list digest -> its records, for persistence and the verifier
}

// newTraceSource builds the source. protected is the operator's protected-component set (the cabinet's own).
func newTraceSource(ids *identityRegistry, trustMode string, protected []string) *traceSource {
	p := map[string]bool{}
	for _, id := range protected {
		p[id] = true
	}
	return &traceSource{identities: ids, trustMode: trustMode, protected: p, archive: map[string][]hostsign.Record{}}
}

// ExecutedIdentity implements cabinet.TraceV2Source. ok=false: the module was not admitted with a profile binding, or the
// artifact is not the admitted one: no executed identity may be claimed.
func (s *traceSource) ExecutedIdentity(m *cabinet.ModuleDescriptor, dispatched string) (cabinet.ExecutedIdentityV2, bool) {
	ident, ok := s.identities.get(m.ID(), m.Checksum())
	if !ok {
		return cabinet.ExecutedIdentityV2{}, false
	}
	ident.Dispatched = dispatched
	return ident, true
}

// recordDigest is the digest of a small canonical record: a fixed header, then key=value lines in the given order.
func recordDigest(header string, kv ...string) string {
	var b strings.Builder
	b.WriteString(header + "\n")
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteString(kv[i] + "=" + kv[i+1] + "\n")
	}
	return sha256Of([]byte(b.String()))
}

// Authorization implements cabinet.TraceV2Source: a protected component is refused at the execution boundary (the cabinet
// has no other authorization decision), and that refusal gets a decision record; everything else is not evaluated.
func (s *traceSource) Authorization(_ context.Context, step cabinet.ExecutionGraphStep) (string, string) {
	if !s.protected[string(step.ComponentID)] {
		return cabinet.AuthorizationNotEvaluated, ""
	}
	return cabinet.AuthorizationDenied, recordDigest("CICAUTHZv1",
		"component", string(step.ComponentID), "decision", "denied", "reason", "protected_operation")
}

// ExecutionContext implements cabinet.TraceV2Source. The record carries what the relay knows; it states that the principal
// and the realization context are not available instead of leaving them out.
func (s *traceSource) ExecutionContext(_ context.Context, workflowID string) string {
	return recordDigest("CICEXECCTXv1",
		"workflow", workflowID, "trust_mode", s.trustMode, "principal", "unavailable", "realization_context", "unavailable")
}

// ScopeStep implements cabinet.StepScoper: the step's host interactions go to a log of their own.
func (s *traceSource) ScopeStep(ctx context.Context, _ cabinet.ExecutionGraphStep) context.Context {
	return hostsign.WithLog(ctx, hostsign.NewLog())
}

// HostInteractions implements cabinet.TraceV2Source: the digest and count of the step's list, denied and failed calls
// included. The records are archived under the digest so that the verifier can be given exactly them.
func (s *traceSource) HostInteractions(ctx context.Context, _ cabinet.ExecutionGraphStep) (string, int) {
	log, ok := hostsign.LogFrom(ctx)
	if !ok {
		return cabinet.EmptyHostInteractionsDigestV2(), 0
	}
	recs := log.Records()
	d, err := hostsign.InteractionsDigest(recs)
	if err != nil {
		// a list that cannot be digested is not evidence: claim none rather than a wrong digest (it cannot happen for
		// records the engine wrote, which are contiguous from 0)
		return cabinet.EmptyHostInteractionsDigestV2(), 0
	}
	s.mu.Lock()
	s.archive[d] = recs
	s.mu.Unlock()
	return d, len(recs)
}

// Interactions returns the archived records of an interaction-list digest.
func (s *traceSource) Interactions(digest string) ([]hostsign.Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.archive[digest]
	return r, ok
}

// traceSink persists every sealed artifact and the interaction records it references into dir. The files are plain
// evidence, NOT yet an attested persistence object (the recorder attestation is INT-10's identity work).
func (s *traceSource) traceSink(dir string, logger *logrus.Logger) cabinet.TraceV2Sink {
	return func(_ context.Context, a *cabinet.ProofArtifactV2) {
		name := strings.TrimPrefix(a.ChainHash, "sha256:")
		doc, err := json.Marshal(a)
		if err != nil {
			logger.WithError(err).Error("trace v2: artifact not persisted")
			return
		}
		lists := map[string][]hostsign.Record{}
		for _, st := range a.Steps {
			if recs, ok := s.Interactions(st.References.HostInteractions.Digest); ok {
				lists[st.References.HostInteractions.Digest] = recs
			}
		}
		lj, err := json.Marshal(lists)
		if err != nil {
			logger.WithError(err).Error("trace v2: interactions not persisted")
			return
		}
		for suffix, data := range map[string][]byte{".json": doc, ".interactions.json": lj} {
			if err := os.WriteFile(filepath.Join(dir, name+suffix), data, 0o600); err != nil {
				logger.WithError(err).Error(fmt.Sprintf("trace v2: %s not persisted", name+suffix))
			}
		}
	}
}

// traceV2DirEnv enables ProofTrace V2 recording: the directory the sealed artifacts and their interaction records are
// written to. Unset: V2 is off and the V1 path is exactly as before (production activation stays an operator decision).
const traceV2DirEnv = "CIC_TRACE_V2_DIR"

// traceV2OptionsFromEnv returns the cabinet options of the V2 recording, or none when it is not enabled.
func traceV2OptionsFromEnv(logger *logrus.Logger, protected []string) ([]cabinet.CabinetOption, error) {
	dir := strings.TrimSpace(os.Getenv(traceV2DirEnv))
	if dir == "" {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("%s: %w", traceV2DirEnv, err)
	}
	src := newTraceSource(moduleIdentities, strings.TrimSpace(os.Getenv("CIC_TRUST_MODE")), protected)
	logger.WithField("dir", dir).Info("ProofTrace V2 recording enabled")
	return []cabinet.CabinetOption{cabinet.WithTraceV2(src, src.traceSink(dir, logger))}, nil
}
