// Package realization is the realization context of an observation (O-1 G-3): an authenticated, content-addressed object that
// says in which concrete infrastructure environment an abstract object is realised or observed: which provider, region, host,
// API base path and scope.
//
// It is NOT part of the module manifest, NOT a modification of the intent and NOT a property of the ABI profile: it is an
// external input the operator pins. Authentication is the operator's pin of the content digest (the object does not vouch
// for itself), exactly as for the trust bundle. The authorization decision and the execution context record carry its
// digest, so a verifier can tell which environment a decision was about.
//
// This is the MINIMAL context the observation PoC needs. It is the only realization model: there is no second one, and it
// carries nothing about changing infrastructure.
package realization

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"

	"centralrelay/pkg/strictjson"
)

// Kind is the wire kind.
const Kind = "CICRealizationContext"

// Result codes.
const (
	// CodeNotPinned: the presented bytes are not the object the operator pinned.
	CodeNotPinned = "REALIZATION_NOT_PINNED"
	// CodeInvalid: the pinned bytes do not have the exact wire shape.
	CodeInvalid = "REALIZATION_INVALID"
)

// Error is a named admission failure.
type Error struct{ Code, Message string }

// Error renders the code and the message.
func (e *Error) Error() string { return e.Code + ": " + e.Message }

// fail builds a named admission error.
func fail(code, format string, a ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

var (
	// digestRE is the closed form of a digest.
	digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	// idRE bounds identifiers: lowercase, digits, dot, dash, colon.
	idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,127}$`)
	// hostRE is a lowercase DNS name.
	hostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	// pathRE is an absolute path without query, fragment, whitespace or dot segments.
	pathRE = regexp.MustCompile(`^/[A-Za-z0-9._~/-]{0,255}$`)
	// scopeRE is a provider scope identifier (for example an OCID): printable, no whitespace.
	scopeRE = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,256}$`)
)

// shape is the exact wire shape of the context.
var shape = &strictjson.Shape{
	Keys:     map[string]*strictjson.Shape{"kind": nil, "version": nil, "context_id": nil, "provider": nil, "region": nil, "host": nil, "base_path": nil, "scope_id": nil},
	Required: []string{"kind", "version", "context_id", "provider", "region", "host", "base_path", "scope_id"},
}

// Context is the realization context.
type Context struct {
	Kind      string `json:"kind"`
	Version   int    `json:"version"`
	ContextID string `json:"context_id"`
	Provider  string `json:"provider"`
	Region    string `json:"region"`
	Host      string `json:"host"`
	BasePath  string `json:"base_path"`
	ScopeID   string `json:"scope_id"`
}

// Admitted is a context that passed admission, with its content digest.
type Admitted struct {
	Digest  string
	Context Context
}

// Digest is the sha256:<hex> of the raw bytes: the content address.
func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Admit checks raw against the operator's pinned digest (first, on the raw bytes) and then the exact wire shape.
func Admit(raw []byte, pinnedDigest string) (*Admitted, error) {
	if !digestRE.MatchString(pinnedDigest) {
		return nil, fail(CodeNotPinned, "the operator has not pinned a realization context digest (sha256:<64 lowercase hex>)")
	}
	got := Digest(raw)
	if got != pinnedDigest {
		return nil, fail(CodeNotPinned, "the presented realization context is %s, the operator pinned %s", got, pinnedDigest)
	}
	if err := strictjson.Scan(raw, shape); err != nil {
		return nil, fail(CodeInvalid, "%v", err)
	}
	var c Context
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fail(CodeInvalid, "%v", err)
	}
	for _, chk := range []struct {
		ok  bool
		msg string
	}{
		{c.Kind == Kind, "kind must be " + Kind},
		{c.Version == 1, "version must be 1"},
		{idRE.MatchString(c.ContextID), "context_id is malformed"},
		{idRE.MatchString(c.Provider), "provider is malformed"},
		{idRE.MatchString(c.Region), "region is malformed"},
		{hostRE.MatchString(c.Host), "host is not a lowercase DNS name"},
		{pathRE.MatchString(c.BasePath), "base_path is not an absolute path"},
		{scopeRE.MatchString(c.ScopeID), "scope_id is malformed"},
	} {
		if !chk.ok {
			return nil, fail(CodeInvalid, "%s", chk.msg)
		}
	}
	return &Admitted{Digest: got, Context: c}, nil
}
