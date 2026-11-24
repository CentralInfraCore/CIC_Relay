// Package cidag builds the deterministic Merkle DAG of a CI process: one canonical object per CI step
// (its definition, inputs, dependency roots and result) and one CI root over all of them.
//
// The contract is docs/hu/CI-MERKLE-DAG-CONTRACT.md. It is additive: it changes no existing hash, root or
// canonical form. The canonical bytes come from pkg/canonicaljson (the CIC Canonical Object Encoding); this
// package only restricts what may be written to a profile on which that encoding can never apply an
// escape (so the escaping rules it inherits from encoding/json never matter), hashes with versioned domain
// prefixes in the style of core/cabinet.ComputeChainHashV1, and wires the dependency structure.
//
// It deliberately does not reuse pkg/merkle.MerkleRoot: that tree duplicates the last element of an odd
// level, so root([a,b,c]) == root([a,b,c,c]), which is harmless for its fixed field set and wrong for a
// variable-length list of steps.
//
// The independent implementation is tests/test_tools/cidag_independent.py; both read testdata/vectors.json.
package cidag

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"

	"centralrelay/pkg/canonicaljson"
)

// Contract identifiers (members of the hashed objects: they separate the domains by content).
const (
	// StepContract identifies the canonical object of one CI step.
	StepContract = "cic-ci-step/1"
	// RootContract identifies the canonical object of the CI root.
	RootContract = "cic-ci-root/1"
	// TemplateContract identifies the canonical object of the template.
	TemplateContract = "cic-ci-template/1"
)

// Domain prefixes, hashed in front of the canonical bytes, followed by a 0x00 byte (the style of CICCHAINv1).
var (
	// stepPrefix is hashed before the canonical bytes of a step object.
	stepPrefix = []byte("CICSTEPv1\x00")
	// templatePrefix is hashed before the canonical bytes of the template object.
	templatePrefix = []byte("CICTEMPLATEv1\x00")
	// rootPrefix is hashed before the canonical bytes of the CI root object.
	rootPrefix = []byte("CICCIROOTv1\x00")
)

// Error classes. They are the same strings the independent implementation and the vectors use.
const (
	// KindParse: the input is not a document the strict reader accepts.
	KindParse = "PARSE"
	// KindProfile: a value is outside the escape-free profile.
	KindProfile = "PROFILE"
	// KindTemplateShape: the template or a step does not have exactly the members of the contract.
	KindTemplateShape = "TEMPLATE_SHAPE"
	// KindTemplateEmpty: the template has no steps.
	KindTemplateEmpty = "TEMPLATE_EMPTY"
	// KindTemplateID: an identifier is not of the permitted form.
	KindTemplateID = "TEMPLATE_ID"
	// KindTemplateDigest: a definition digest is not sha256: and 64 lowercase hexadecimal digits.
	KindTemplateDigest = "TEMPLATE_DIGEST"
	// KindTemplateDuplicateID: two steps share an identifier.
	KindTemplateDuplicateID = "TEMPLATE_DUPLICATE_ID"
	// KindTemplateNeedUnknown: a step needs a step that does not exist.
	KindTemplateNeedUnknown = "TEMPLATE_NEED_UNKNOWN"
	// KindTemplateNeedDuplicate: a step lists the same need twice.
	KindTemplateNeedDuplicate = "TEMPLATE_NEED_DUPLICATE"
	// KindTemplateCycle: the dependency graph has a cycle.
	KindTemplateCycle = "TEMPLATE_CYCLE"
	// KindEvidenceMissing: a step of the template has no evidence (a step that did not run is skipped, not absent).
	KindEvidenceMissing = "EVIDENCE_MISSING"
	// KindEvidenceExtra: there is evidence for a step that is not in the template.
	KindEvidenceExtra = "EVIDENCE_EXTRA"
	// KindEvidenceShape: evidence does not have exactly the members of the contract.
	KindEvidenceShape = "EVIDENCE_SHAPE"
	// KindInputName: an input name is not of the permitted form.
	KindInputName = "INPUT_NAME"
	// KindInputValue: an input value is outside the permitted types and ranges.
	KindInputValue = "INPUT_VALUE"
	// KindResultShape: a result does not have exactly status and digest.
	KindResultShape = "RESULT_SHAPE"
	// KindResultStatus: a status is not one of the permitted conclusions.
	KindResultStatus = "RESULT_STATUS"
	// KindResultDigest: a result digest has the wrong form, or the wrong null-ness for its status.
	KindResultDigest = "RESULT_DIGEST"
	// KindBundleShape: a published bundle does not have exactly the members of the contract.
	KindBundleShape = "BUNDLE_SHAPE"
	// KindObjectShape: a published step object does not have exactly the members of the contract.
	KindObjectShape = "OBJECT_SHAPE"
	// KindObjectContractMismatch: a published object names another contract.
	KindObjectContractMismatch = "OBJECT_CONTRACT_MISMATCH"
	// KindObjectStepMismatch: a published object's step identity differs from the template's.
	KindObjectStepMismatch = "OBJECT_STEP_MISMATCH"
	// KindObjectDependenciesMismatch: a published object's dependency roots differ from the recomputed ones.
	KindObjectDependenciesMismatch = "OBJECT_DEPENDENCIES_MISMATCH"
	// KindStepRootMismatch: the published step roots differ from the recomputed ones.
	KindStepRootMismatch = "STEP_ROOT_MISMATCH"
	// KindCIRootMismatch: the published CI root differs from the recomputed one.
	KindCIRootMismatch = "CI_ROOT_MISMATCH"
)

// Error is a refusal with a stable class.
type Error struct {
	Kind   string
	Detail string
}

// Error returns the class and the detail of the refusal.
func (e *Error) Error() string { return e.Kind + ": " + e.Detail }

// fail returns an *Error of the given class with a formatted detail.
func fail(kind, format string, args ...any) error {
	return &Error{Kind: kind, Detail: fmt.Sprintf(format, args...)}
}

// Statuses are the conclusions a CI check can have (the set of cic-primitive-forms tools/test-evidence/record.py).
var statuses = map[string]bool{
	"success": true, "failure": true, "cancelled": true, "skipped": true,
	"neutral": true, "timed_out": true, "action_required": true, "stale": true,
}

// nullDigestStatuses are the statuses whose result has no digest (nothing was produced): the digest is null.
var nullDigestStatuses = map[string]bool{"skipped": true, "cancelled": true}

const (
	// maxInt is the largest magnitude of an integer the profile accepts (2^53-1).
	maxInt = int64(1)<<53 - 1
	// maxParam is the longest string an input value may be.
	maxParam = 256
	// MaxDepth is the deepest nesting the strict reader accepts.
	MaxDepth = 64
)

var (
	// idRe is the form of a step id, a workflow id and an input name.
	idRe = regexp.MustCompile(`\A[A-Za-z0-9][A-Za-z0-9._:/@+=-]{0,127}\z`)
	// digestRe is the form of a digest: sha256: and 64 lowercase hexadecimal digits.
	digestRe = regexp.MustCompile(`\Asha256:[0-9a-f]{64}\z`)
	// intRe is the form of an integer literal the strict reader accepts.
	intRe = regexp.MustCompile(`\A-?(0|[1-9][0-9]*)\z`)
)

// paramChar reports whether a character of a string is inside the escape-free profile: printable ASCII
// without the characters a JSON writer would escape or an HTML-safe encoder would rewrite (" \ < > &).
func paramChar(c rune) bool {
	if c < 0x20 || c > 0x7e {
		return false
	}
	switch c {
	case '"', '\\', '<', '>', '&':
		return false
	}
	return true
}

// paramString reports whether every character of s is inside the escape-free profile.
func paramString(s string) bool {
	for _, c := range s {
		if !paramChar(c) {
			return false
		}
	}
	return true
}

// asInt returns the value of a JSON integer (not a bool) held in any Go integer type the package produces.
func asInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int:
		return int64(x), true
	case int64:
		return x, true
	}
	return 0, false
}

// checkProfile refuses any value outside the profile: strings that need no escape, integers within +-(2^53-1),
// booleans and null, arrays and objects of those.
func checkProfile(v any) error {
	switch x := v.(type) {
	case nil, bool:
		return nil
	case int, int64:
		n, _ := asInt(x)
		if n > maxInt || n < -maxInt {
			return fail(KindProfile, "integer beyond 2^53-1")
		}
		return nil
	case string:
		if !paramString(x) {
			return fail(KindProfile, "string outside the escape-free profile: %q", x)
		}
		return nil
	case []any:
		for _, e := range x {
			if err := checkProfile(e); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for k, e := range x {
			if !paramString(k) {
				return fail(KindProfile, "key outside the escape-free profile: %q", k)
			}
			if err := checkProfile(e); err != nil {
				return err
			}
		}
		return nil
	}
	return fail(KindProfile, "unsupported type %T", v)
}

// Canonical returns the canonical bytes of a value of the profile (pkg/canonicaljson.ToJSON, after the
// profile check).
func Canonical(v any) ([]byte, error) {
	if err := checkProfile(v); err != nil {
		return nil, err
	}
	return canonicaljson.ToJSON(v)
}

// digest returns "sha256:" + hex(SHA-256(prefix || canonical(v))) and the canonical bytes.
func digest(prefix []byte, v any) (string, []byte, error) {
	b, err := Canonical(v)
	if err != nil {
		return "", nil, err
	}
	h := sha256.New()
	h.Write(prefix)
	h.Write(b)
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), b, nil
}
