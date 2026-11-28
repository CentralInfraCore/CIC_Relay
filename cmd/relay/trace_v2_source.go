package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"

	"centralrelay/core/cabinet"
	"centralrelay/pkg/hostsign"
	"centralrelay/pkg/o1verify"
	"centralrelay/pkg/o1verify/attest"
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
	// decisions and contexts keep the records behind the digests the artifact references, so that a verifier can check their content.
	decisions map[string]cabinet.AuthorizationRecordV2
	contexts  map[string]map[string]string
}

// newTraceSource builds the source. protected is the operator's protected-component set (the cabinet's own).
func newTraceSource(ids *identityRegistry, trustMode string, protected []string) *traceSource {
	p := map[string]bool{}
	for _, id := range protected {
		p[id] = true
	}
	return &traceSource{identities: ids, trustMode: trustMode, protected: p, archive: map[string][]hostsign.Record{}, decisions: map[string]cabinet.AuthorizationRecordV2{}, contexts: map[string]map[string]string{}}
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

// Authorization implements cabinet.TraceV2Source: a protected component is refused at the execution boundary (the cabinet
// has no other authorization decision outside the profile gate, which records its own), and that refusal gets an
// authorization record; everything else is not evaluated here. The record states "none" for what a refusal before
// admission cannot know (no envelope, no profile, no input).
func (s *traceSource) Authorization(ctx context.Context, step cabinet.ExecutionGraphStep) (string, string) {
	if !s.protected[string(step.ComponentID)] {
		return cabinet.AuthorizationNotEvaluated, ""
	}
	p, ok := cabinet.PrincipalFrom(ctx)
	r := cabinet.AuthorizationRecordV2{
		ModuleID: string(step.ComponentID), EnvelopeDigest: cabinet.None, ProfileID: cabinet.None, ProfileDigest: cabinet.None,
		Operation: step.FunctionName, HostBoundary: cabinet.None, Effect: cabinet.None, EffectResolved: cabinet.None,
		Principal: cabinet.PrincipalString(p, ok), RealizationContextDigest: realizationDigest(), PolicyDigest: cabinet.None,
		Decision: cabinet.AuthorizationDenied, Reason: "protected_operation", WorkflowID: cabinet.None, StepIndex: 0, InputDigest: cabinet.None,
	}
	d := r.Digest()
	s.ArchiveDecision(ctx, d, r)
	return cabinet.AuthorizationDenied, d
}

// ArchiveDecision implements cabinet.DecisionArchiver: the cabinet's own decision records are kept for the verifier.
func (s *traceSource) ArchiveDecision(_ context.Context, digest string, r cabinet.AuthorizationRecordV2) {
	s.mu.Lock()
	s.decisions[digest] = r
	s.mu.Unlock()
}

// executionContextText is the canonical text of the execution-context record (CICEXECCTXv2).
func executionContextText(f map[string]string) string {
	var b strings.Builder
	b.WriteString("CICEXECCTXv2\n")
	for _, k := range []string{"workflow_id", "trust_mode", "principal", "realization_context_digest"} {
		b.WriteString(k + "=" + strings.NewReplacer("\n", " ", "\r", " ").Replace(f[k]) + "\n")
	}
	return b.String()
}

// ExecutionContext implements cabinet.TraceV2Source. The record carries what the relay knows, from the authentication layer
// and the operator's pinned realization context, and says "unavailable" or "none" where there is nothing: the principal's
// type, id and method are never taken from the request payload.
func (s *traceSource) ExecutionContext(ctx context.Context, workflowID string) string {
	p, ok := cabinet.PrincipalFrom(ctx)
	return s.putContext(map[string]string{"workflow_id": workflowID, "trust_mode": s.trustMode, "principal": cabinet.PrincipalString(p, ok), "realization_context_digest": realizationDigest()})
}

// putContext archives an execution-context record and returns its digest.
func (s *traceSource) putContext(f map[string]string) string {
	d := sha256Of([]byte(executionContextText(f)))
	s.mu.Lock()
	s.contexts[d] = f
	s.mu.Unlock()
	return d
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

// recorderKey is the key a recorder attests persistence with. The attestation is only ever made AFTER the artifact and its
// interaction lists were written and flushed: a recorder that attested before the write would be attesting a hope.
type recorderKey struct {
	priv ed25519.PrivateKey
	spki []byte
	id   string
}

// loadRecorderKey reads a PKCS#8 PEM ed25519 private key.
func loadRecorderKey(path string) (*recorderKey, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- operator-supplied key path
	if err != nil {
		return nil, fmt.Errorf("read recorder key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("recorder key: not PEM")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("recorder key: %w", err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("recorder key: only ed25519 is supported")
	}
	spki, err := x509.MarshalPKIXPublicKey(priv.Public())
	if err != nil {
		return nil, err
	}
	return &recorderKey{priv: priv, spki: spki, id: sha256Of(spki)}, nil
}

// writeDurable writes data to dir/name atomically and durably: temp file, flush, rename, flush the directory.
func writeDurable(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".v2-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	d, err := os.Open(dir) // #nosec G304 -- the evidence directory
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// persist writes the artifact and its interaction lists durably and returns the artifact file's digest.
func (s *traceSource) persist(dir string, a *cabinet.ProofArtifactV2) (string, error) {
	name := strings.TrimPrefix(a.ChainHash, "sha256:")
	doc, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	lists := map[string][]hostsign.Record{}
	for _, st := range a.Steps {
		if recs, ok := s.Interactions(st.References.HostInteractions.Digest); ok {
			lists[st.References.HostInteractions.Digest] = recs
		}
	}
	lj, err := json.Marshal(lists)
	if err != nil {
		return "", err
	}
	decs := map[string]cabinet.AuthorizationRecordV2{}
	ctxs := map[string]map[string]string{}
	s.mu.Lock()
	for _, st := range a.Steps {
		if st.References.AuthorizationDecision != nil {
			if r, ok := s.decisions[st.References.AuthorizationDecision.Digest]; ok {
				decs[st.References.AuthorizationDecision.Digest] = r
			}
		}
		if c, ok := s.contexts[st.References.ExecutionContext.Digest]; ok {
			ctxs[st.References.ExecutionContext.Digest] = c
		}
	}
	s.mu.Unlock()
	dj, err := json.Marshal(decs)
	if err != nil {
		return "", err
	}
	cj, err := json.Marshal(ctxs)
	if err != nil {
		return "", err
	}
	if err := writeDurable(dir, name+".decisions.json", dj); err != nil {
		return "", err
	}
	if err := writeDurable(dir, name+".contexts.json", cj); err != nil {
		return "", err
	}
	if err := writeDurable(dir, name+".json", doc); err != nil {
		return "", err
	}
	if err := writeDurable(dir, name+".interactions.json", lj); err != nil {
		return "", err
	}
	return sha256Of(doc), nil
}

// traceSink persists every sealed artifact and the interaction lists it references into dir. With a recorder key it then,
// and only after both writes succeeded, writes the persistence object and the attestation signature material
// (<chain>.persistence.json, <chain>.attestation.json); when a write failed it records persisted=false with an error code
// and NO attestation. Without a key the files are plain evidence: no persistence is claimed.
func (s *traceSource) traceSink(dir string, rk *recorderKey, logger *logrus.Logger) cabinet.TraceV2Sink {
	return func(_ context.Context, a *cabinet.ProofArtifactV2) {
		name := strings.TrimPrefix(a.ChainHash, "sha256:")
		fileDigest, err := s.persist(dir, a)
		if err != nil {
			logger.WithError(err).Error("trace v2: not persisted")
			if rk != nil {
				p := map[string]any{"artifact_chain_hash": a.ChainHash, "persisted": false, "recorder": "relay", "error_code": "PERSIST_FAILED"}
				if pj, merr := json.Marshal(p); merr == nil {
					_ = writeDurable(dir, name+".persistence.json", pj)
				}
			}
			return
		}
		if rk == nil {
			return
		}
		sig := ed25519.Sign(rk.priv, attest.RecorderSignedBytes(a.ChainHash, rk.id, attest.AlgEd25519))
		p := o1verify.Persistence{ArtifactChainHash: a.ChainHash, Persisted: true, Recorder: "relay", Commit: "file:" + fileDigest,
			Attestation: &o1verify.Attestation{Kind: "recorder-signature", SignerKeyID: rk.id, SignatureDigest: sha256Of(sig)}}
		pj, err := json.Marshal(p)
		if err != nil {
			logger.WithError(err).Error("trace v2: persistence object not written")
			return
		}
		mj, err := json.Marshal(map[string]string{"signature_b64": base64.StdEncoding.EncodeToString(sig), "spki_b64": base64.StdEncoding.EncodeToString(rk.spki)})
		if err != nil {
			return
		}
		for n, data := range map[string][]byte{name + ".attestation.json": mj, name + ".persistence.json": pj} {
			if err := writeDurable(dir, n, data); err != nil {
				logger.WithError(err).Error("trace v2: " + n + " not written")
				return
			}
		}
	}
}

// traceV2DirEnv enables ProofTrace V2 recording: the directory the sealed artifacts and their interaction records are
// written to. Unset: V2 is off and the V1 path is exactly as before (production activation stays an operator decision).
const traceV2DirEnv = "CIC_TRACE_V2_DIR"

// traceV2RecorderKeyEnv is the path of the recorder's PKCS#8 PEM ed25519 key. Unset: artifacts are stored but no persistence
// is claimed or attested. The key must hold recorder:attest in the pinned trust bundle for a verifier to accept the attestation.
const traceV2RecorderKeyEnv = "CIC_TRACE_V2_RECORDER_KEY"

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
	var rk *recorderKey
	if kp := strings.TrimSpace(os.Getenv(traceV2RecorderKeyEnv)); kp != "" {
		var err error
		if rk, err = loadRecorderKey(kp); err != nil {
			return nil, err
		}
	}
	logger.WithFields(logrus.Fields{"dir": dir, "attesting": rk != nil}).Info("ProofTrace V2 recording enabled")
	return []cabinet.CabinetOption{cabinet.WithTraceV2(src, src.traceSink(dir, rk, logger))}, nil
}
