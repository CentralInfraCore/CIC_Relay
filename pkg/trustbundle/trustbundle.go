// Package trustbundle admits the operator's trust bundle (O-1 INT-12; contract: release-admission-contract-draft 9.2).
//
// The bundle carries what the relay must take from the operator's trust domain: which signer keys hold module:sign
// (authority bindings) and which ABI profiles are accepted (id, digest). It does NOT authenticate itself: whatever it
// contains, it is only as good as the operator's pin. Admission is therefore four independent checks, in this order,
// each with its own result code:
//
//	BUNDLE_NOT_PINNED  the presented bytes are not the bundle whose sha256 the operator pinned (checked first and on the
//	                   raw bytes: nothing of an unpinned bundle is trusted, not even its version or its age)
//	BUNDLE_INVALID     the pinned bytes do not have the exact wire shape (strict JSON, closed vocabulary, no duplicates)
//	BUNDLE_STALE       bundle_version is below the operator's minimum_bundle_version
//	BUNDLE_ROLLBACK    bundle_version is below the highest version this relay has already admitted (anti-rollback)
//	BUNDLE_EXPIRED     the bundle is older than the operator's max age, or is issued in the future
//
// The first two codes and the minimum version are the draft's; the anti-rollback high-water mark and the age check are the
// owner's INT-12 list (digest pin, minimum version, freshness, anti-rollback) given a code each. The high-water mark
// is advanced by the caller only through Commit, after every check passed, and is persisted atomically.
package trustbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"centralrelay/pkg/strictjson"
)

// Kind is the wire kind of the bundle.
const Kind = "CICTrustBundle"

// RoleModuleSign is the authority a signer key must hold to sign module releases (#162 V2b).
const RoleModuleSign = "module:sign"

// MaxClockSkew is how far in the future an issued_at may lie before the bundle is refused.
const MaxClockSkew = 5 * time.Minute

// Result codes.
const (
	// CodeNotPinned: the presented bytes are not the bundle the operator pinned.
	CodeNotPinned = "BUNDLE_NOT_PINNED"
	// CodeInvalid: the bundle does not have the exact wire shape.
	CodeInvalid = "BUNDLE_INVALID"
	// CodeStale: bundle_version is below the operator's minimum.
	CodeStale = "BUNDLE_STALE"
	// CodeRollback: bundle_version is below the highest admitted version.
	CodeRollback = "BUNDLE_ROLLBACK"
	// CodeExpired: the bundle is older than the operator's limit or issued in the future.
	CodeExpired = "BUNDLE_EXPIRED"
)

// Error is a named admission failure.
type Error struct {
	Code    string
	Message string
}

// Error renders the code and the message.
func (e *Error) Error() string { return e.Code + ": " + e.Message }

// fail builds a named admission error.
func fail(code, format string, a ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// CodeOf returns the result code of an admission error ("" for any other error).
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

var (
	// digestRE is the closed form of a digest: sha256: and 64 lowercase hex.
	digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// idRE is the closed form of a profile id.
	idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
)

// bundleShape is the exact wire shape of the bundle.
var bundleShape = &strictjson.Shape{
	Keys: map[string]*strictjson.Shape{
		"kind": nil, "bundle_version": nil, "issued_at": nil,
		"authorities":       {Elem: &strictjson.Shape{Keys: map[string]*strictjson.Shape{"key_id": nil, "roles": nil}, Required: []string{"key_id", "roles"}}},
		"accepted_profiles": {Elem: &strictjson.Shape{Keys: map[string]*strictjson.Shape{"id": nil, "digest": nil}, Required: []string{"id", "digest"}}},
	},
	Required: []string{"kind", "bundle_version", "issued_at", "authorities", "accepted_profiles"},
}

// Policy is what the operator pins; it is not read from the bundle.
type Policy struct {
	// PinnedDigest is the sha256:<hex> of the exact bundle bytes the operator accepts. Required.
	PinnedDigest string
	// MinVersion is minimum_bundle_version: a bundle below it is stale.
	MinVersion uint64
	// MaxAge bounds now - issued_at; zero means no age limit.
	MaxAge time.Duration
}

// State is the relay's persisted admission state.
type State struct {
	// HighWatermark is the highest bundle_version this relay has admitted.
	HighWatermark uint64 `json:"high_watermark"`
}

// Profile is one accepted ABI profile pin.
type Profile struct{ ID, Digest string }

// Admitted is an admitted bundle's content.
type Admitted struct {
	Digest   string
	Version  uint64
	IssuedAt time.Time
	// ModuleSign holds, per signer key_id, whether module:sign is granted.
	ModuleSign map[string]bool
	Profiles   []Profile
}

// wire is the decoded bundle, read only after the strict scan.
type wire struct {
	Kind        string `json:"kind"`
	Version     uint64 `json:"bundle_version"`
	IssuedAt    string `json:"issued_at"`
	Authorities []struct {
		KeyID string   `json:"key_id"`
		Roles []string `json:"roles"`
	} `json:"authorities"`
	Profiles []struct {
		ID     string `json:"id"`
		Digest string `json:"digest"`
	} `json:"accepted_profiles"`
}

// Digest is the sha256:<hex> of the raw bundle bytes.
func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Admit checks raw against the operator's policy and the relay's state at time now. It does not change the state.
func Admit(raw []byte, pol Policy, st State, now time.Time) (*Admitted, error) {
	if !digestRE.MatchString(pol.PinnedDigest) {
		return nil, fail(CodeNotPinned, "the operator has not pinned a bundle digest (sha256:<64 lowercase hex>)")
	}
	got := Digest(raw)
	if got != pol.PinnedDigest {
		return nil, fail(CodeNotPinned, "the presented bundle is %s, the operator pinned %s", got, pol.PinnedDigest)
	}
	if err := strictjson.Scan(raw, bundleShape); err != nil {
		return nil, fail(CodeInvalid, "%v", err)
	}
	var w wire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fail(CodeInvalid, "%v", err)
	}
	if w.Kind != Kind {
		return nil, fail(CodeInvalid, "kind must be %s", Kind)
	}
	issued, err := time.Parse(time.RFC3339, w.IssuedAt)
	if err != nil {
		return nil, fail(CodeInvalid, "issued_at is not RFC 3339: %v", err)
	}
	out := &Admitted{Digest: got, Version: w.Version, IssuedAt: issued, ModuleSign: map[string]bool{}}
	for i, a := range w.Authorities {
		if !digestRE.MatchString(a.KeyID) {
			return nil, fail(CodeInvalid, "authorities[%d].key_id is not sha256:<64 lowercase hex>", i)
		}
		if _, dup := out.ModuleSign[a.KeyID]; dup {
			return nil, fail(CodeInvalid, "authorities[%d]: duplicate key_id", i)
		}
		granted := false
		for _, r := range a.Roles {
			if r != RoleModuleSign {
				return nil, fail(CodeInvalid, "authorities[%d]: unknown role %q", i, clip(r))
			}
			granted = true
		}
		out.ModuleSign[a.KeyID] = granted
	}
	seen := map[string]bool{}
	for i, p := range w.Profiles {
		if !idRE.MatchString(p.ID) || !digestRE.MatchString(p.Digest) {
			return nil, fail(CodeInvalid, "accepted_profiles[%d]: id or digest is malformed", i)
		}
		if seen[p.ID] {
			return nil, fail(CodeInvalid, "accepted_profiles[%d]: duplicate profile id", i)
		}
		seen[p.ID] = true
		out.Profiles = append(out.Profiles, Profile{ID: p.ID, Digest: p.Digest})
	}
	if w.Version < pol.MinVersion {
		return nil, fail(CodeStale, "bundle_version %d is below the minimum %d", w.Version, pol.MinVersion)
	}
	if w.Version < st.HighWatermark {
		return nil, fail(CodeRollback, "bundle_version %d is below the highest admitted version %d", w.Version, st.HighWatermark)
	}
	if issued.After(now.Add(MaxClockSkew)) {
		return nil, fail(CodeExpired, "issued_at %s is in the future", w.IssuedAt)
	}
	if pol.MaxAge > 0 && now.Sub(issued) > pol.MaxAge {
		return nil, fail(CodeExpired, "the bundle is %s old, the maximum is %s", now.Sub(issued).Round(time.Second), pol.MaxAge)
	}
	return out, nil
}

// clip bounds an offending value before it is repeated in an error message.
func clip(s string) string {
	if len(s) > 64 {
		return s[:64] + "..."
	}
	return s
}

// LoadState reads the persisted state. A missing file is the zero state (first start); an unreadable or malformed file
// is an error, never a silent reset: resetting the high-water mark would re-open a rollback.
func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- operator-supplied state path
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read trust-bundle state: %w", err)
	}
	if err := strictjson.Scan(data, &strictjson.Shape{Keys: map[string]*strictjson.Shape{"high_watermark": nil}, Required: []string{"high_watermark"}}); err != nil {
		return State{}, fmt.Errorf("trust-bundle state: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("trust-bundle state: %w", err)
	}
	return st, nil
}

// Commit raises the high-water mark to the admitted version and persists it atomically (write, sync, rename). It never
// lowers it. Call it only after Admit succeeded.
func Commit(path string, st State, adm *Admitted) (State, error) {
	if adm.Version <= st.HighWatermark {
		return st, nil
	}
	st.HighWatermark = adm.Version
	data, err := json.Marshal(st)
	if err != nil {
		return st, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tb-state-*")
	if err != nil {
		return st, fmt.Errorf("persist trust-bundle state: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return st, fmt.Errorf("persist trust-bundle state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return st, fmt.Errorf("persist trust-bundle state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return st, fmt.Errorf("persist trust-bundle state: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return st, fmt.Errorf("persist trust-bundle state: %w", err)
	}
	return st, nil
}
