package iac

// Field-level access control (feature-011, iac-object-model.md, spec #2). The doc's
// load-bearing decision is to pick ONE formal model, not a POSIX/NFSv4/RBAC blend.
// This is cic-acl/v1: POSIX-style default-deny with owner / group / other classes,
// named allow entries, a mask that caps the named+group classes, and inheritance.
// There is deliberately NO explicit deny in v1 (that is cic-acl/v2) — every entry
// grants, and access is denied by absence.
//
// Evaluation is "most specific class wins": the owner class, then a matching named
// user, then the group class (owning group ∪ matching named groups), then other.
// A matched class that lacks the requested bit denies — evaluation does not fall
// through to a broader class. Owner and other bypass the mask; named users and the
// group class are capped by it. ACLs key on [FieldID] (via [ACLSet]) so ownership
// survives a path rename.

// Permission is a bitset of the access bits.
type Permission uint8

const (
	// PermRead — read the field's value.
	PermRead Permission = 1 << iota
	// PermWrite — set the field's value (a config/intent write).
	PermWrite
	// PermExecute — act on the field (e.g. trigger a referenced operation).
	PermExecute
)

// allPerms is every bit — the value an unset mask resolves to (no capping).
const allPerms = PermRead | PermWrite | PermExecute

// PrincipalKind distinguishes a user identity from a group.
type PrincipalKind string

const (
	// PrincipalUser is a single actor identity.
	PrincipalUser PrincipalKind = "user"
	// PrincipalGroup is a group of actors.
	PrincipalGroup PrincipalKind = "group"
)

// Principal is a named user or group referenced by an ACL entry.
type Principal struct {
	Kind PrincipalKind
	ID   string
}

// Actor is who is requesting access: a user identity and the groups it belongs to.
type Actor struct {
	User   string
	Groups []string
}

// ACLEntry is a named allow entry — extra permissions for a specific user or group.
type ACLEntry struct {
	Principal Principal
	Perms     Permission
}

// ACL is a field's access-control list (cic-acl/v1, allow-only).
type ACL struct {
	Owner      string     // owning user identity
	Group      string     // owning group
	OwnerPerms Permission // owner class (not masked)
	GroupPerms Permission // owning-group class (masked)
	OtherPerms Permission // everyone else (not masked)
	Mask       Permission // caps named + group classes; zero = unset = no cap
	Named      []ACLEntry // named allow entries (users and groups)
	Inherit    bool       // when true, resolve entirely from the parent ACL
}

// Allows reports whether the actor is granted every bit in want.
func (a ACL) Allows(actor Actor, want Permission) bool {
	return a.effective(actor)&want == want
}

// effective computes the actor's permission bits under most-specific-class-wins.
func (a ACL) effective(actor Actor) Permission {
	// Owner class — bypasses the mask.
	if actor.User != "" && actor.User == a.Owner {
		return a.OwnerPerms
	}
	// Named user — the most specific named match; capped by the mask.
	for _, e := range a.Named {
		if e.Principal.Kind == PrincipalUser && e.Principal.ID == actor.User {
			return e.Perms & a.mask()
		}
	}
	// Group class — owning group ∪ matching named groups; capped by the mask.
	var group Permission
	matched := false
	if a.Group != "" && contains(actor.Groups, a.Group) {
		group |= a.GroupPerms
		matched = true
	}
	for _, e := range a.Named {
		if e.Principal.Kind == PrincipalGroup && contains(actor.Groups, e.Principal.ID) {
			group |= e.Perms
			matched = true
		}
	}
	if matched {
		return group & a.mask()
	}
	// Other — bypasses the mask.
	return a.OtherPerms
}

// mask resolves the effective mask: an unset (zero) mask does not cap.
func (a ACL) mask() Permission {
	if a.Mask == 0 {
		return allPerms
	}
	return a.Mask
}

// Resolve applies inheritance: an ACL marked Inherit takes the parent's ACL
// wholesale; otherwise it is used as-is.
func (a ACL) Resolve(parent ACL) ACL {
	if a.Inherit {
		return parent
	}
	return a
}

// ACLSet maps field identities to their ACL — ownership keyed on [FieldID], not the
// path, so it survives a rename (spec #5).
type ACLSet map[FieldID]ACL

// Effective returns the ACL governing a field id, resolving inheritance against the
// object-level default. A field with no entry uses the object default.
func (s ACLSet) Effective(id FieldID, objectDefault ACL) ACL {
	if acl, ok := s[id]; ok {
		return acl.Resolve(objectDefault)
	}
	return objectDefault
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
