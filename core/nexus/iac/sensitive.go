package iac

import "fmt"

// Secret custody (feature-011, iac-object-model.md, "Secret custody"). `sensitive`
// is a placement/custody policy, NOT an ACL: `access.acl` says *who* may
// reference/rotate/use a value; `sensitive` says *where the secret may live* and
// *how it may be backed up*. The two are kept deliberately separate ([ACL] vs
// [SensitivePolicy]).
//
// The load-bearing invariant (from the thread): a secret never crosses the
// type boundary into plaintext. A field classed `secret` may hold ONLY a
// [SecretRef] (a capability handle), never an inline value; a field classed `none`
// may NOT hold a SecretRef. This is what keeps a secret out of the canonical
// digest — the bytes hashed are always the handle, never the plaintext.

// SensitiveClass is whether a field carries a secret.
type SensitiveClass string

const (
	// SensitiveNone — an ordinary value; a SecretRef is forbidden.
	SensitiveNone SensitiveClass = "none"
	// SensitiveSecret — the value is a secret; only a SecretRef is permitted.
	SensitiveSecret SensitiveClass = "secret"
)

// Exposure is where the plaintext of a secret may exist.
type Exposure string

const (
	// ExposureLocal — plaintext may exist only within the local custody boundary.
	ExposureLocal Exposure = "local"
	// ExposureRemote — plaintext never leaves custody; only a capability handle is
	// exposed.
	ExposureRemote Exposure = "remote"
)

// BackupOp is a management-scope backup level.
type BackupOp string

const (
	// BackupTransit — the secret may be re-wrapped for transit.
	BackupTransit BackupOp = "transit"
	// BackupStore — the secret may be persisted (as ciphertext) at rest.
	BackupStore BackupOp = "store"
	// BackupRestore — the secret may be restored from a backup.
	BackupRestore BackupOp = "restore"
)

// SecretRef is a reference (capability handle) to a secret in custody — never the
// plaintext itself.
type SecretRef struct {
	Path string
}

// asSecretRef recognizes a secret-reference value: a map carrying a non-empty
// "secret_ref" string.
func asSecretRef(v interface{}) (SecretRef, bool) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return SecretRef{}, false
	}
	p, ok := m["secret_ref"].(string)
	if !ok || p == "" {
		return SecretRef{}, false
	}
	return SecretRef{Path: p}, true
}

// AsSecretRef reports whether the field's value is a secret reference.
func (f Field) AsSecretRef() (SecretRef, bool) {
	return asSecretRef(f.Value)
}

// SensitivePolicy is a field's secret placement/custody policy.
type SensitivePolicy struct {
	Class    SensitiveClass
	Exposure Exposure
	Backups  []BackupOp
}

// ValidateValue enforces the secret type boundary: a secret field must hold a
// SecretRef (never plaintext), and a non-secret field must not hold a SecretRef.
func (p SensitivePolicy) ValidateValue(value interface{}) error {
	_, isSecret := asSecretRef(value)
	switch p.Class {
	case SensitiveSecret:
		if !isSecret {
			return fmt.Errorf("secret field must hold a secret reference, not plaintext")
		}
	default: // none / unset
		if isSecret {
			return fmt.Errorf("non-secret field must not hold a secret reference")
		}
	}
	return nil
}

// AllowsBackup reports whether a backup operation is permitted by this policy.
func (p SensitivePolicy) AllowsBackup(op BackupOp) bool {
	for _, b := range p.Backups {
		if b == op {
			return true
		}
	}
	return false
}

// parseSensitive reads a `behavior.sensitive` descriptor into a [SensitivePolicy].
func parseSensitive(raw map[string]interface{}) SensitivePolicy {
	p := SensitivePolicy{}
	if c, ok := raw["class"].(string); ok {
		p.Class = SensitiveClass(c)
	}
	if e, ok := raw["exposure"].(string); ok {
		p.Exposure = Exposure(e)
	}
	for _, b := range asStringSlice(raw["backup"]) {
		p.Backups = append(p.Backups, BackupOp(b))
	}
	return p
}

// ValidateSensitive checks every scalar field's value in obj against its sensitive
// policy, so the secret type boundary is enforced object-wide. (Collection element
// secrets are a later round.)
func (s Schema) ValidateSensitive(obj map[string]interface{}) error {
	for _, f := range s.Fields {
		if f.Collection != nil || f.Sensitive.Class == "" {
			continue
		}
		v, present := resolvePath(obj, f.Path)
		if !present {
			continue
		}
		if err := f.Sensitive.ValidateValue(v); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	return nil
}
