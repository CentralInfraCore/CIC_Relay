package iac

import "testing"

func TestACL_Allows(t *testing.T) {
	cases := []struct {
		name  string
		acl   ACL
		actor Actor
		want  Permission
		allow bool
	}{
		{
			name:  "owner gets owner perms, mask ignored",
			acl:   ACL{Owner: "alice", OwnerPerms: PermRead | PermWrite, Mask: PermRead},
			actor: Actor{User: "alice"},
			want:  PermWrite, allow: true,
		},
		{
			name:  "most-specific-class-wins: owner without bit is denied, no fall-through to other",
			acl:   ACL{Owner: "alice", OwnerPerms: PermRead, OtherPerms: PermRead | PermWrite},
			actor: Actor{User: "alice"},
			want:  PermWrite, allow: false,
		},
		{
			name:  "named user, capped by mask",
			acl:   ACL{Named: []ACLEntry{{Principal{PrincipalUser, "bob"}, PermRead | PermWrite}}, Mask: PermRead},
			actor: Actor{User: "bob"},
			want:  PermWrite, allow: false,
		},
		{
			name:  "named user, read still granted under mask",
			acl:   ACL{Named: []ACLEntry{{Principal{PrincipalUser, "bob"}, PermRead | PermWrite}}, Mask: PermRead},
			actor: Actor{User: "bob"},
			want:  PermRead, allow: true,
		},
		{
			name: "group class is owning-group ∪ named-groups, capped by mask",
			acl: ACL{
				Group: "eng", GroupPerms: PermRead,
				Named: []ACLEntry{{Principal{PrincipalGroup, "ops"}, PermWrite}},
				Mask:  PermRead | PermWrite,
			},
			actor: Actor{User: "carol", Groups: []string{"eng", "ops"}},
			want:  PermWrite, allow: true,
		},
		{
			name: "group class capped: mask removes write",
			acl: ACL{
				Group: "eng", GroupPerms: PermRead,
				Named: []ACLEntry{{Principal{PrincipalGroup, "ops"}, PermWrite}},
				Mask:  PermRead,
			},
			actor: Actor{User: "carol", Groups: []string{"eng", "ops"}},
			want:  PermWrite, allow: false,
		},
		{
			name:  "unset mask does not cap",
			acl:   ACL{Named: []ACLEntry{{Principal{PrincipalUser, "bob"}, PermRead | PermWrite}}},
			actor: Actor{User: "bob"},
			want:  PermRead | PermWrite, allow: true,
		},
		{
			name:  "other class for a non-matching actor",
			acl:   ACL{Owner: "alice", OtherPerms: PermRead},
			actor: Actor{User: "dave", Groups: []string{"randoms"}},
			want:  PermRead, allow: true,
		},
		{
			name:  "default-deny: empty ACL grants nothing",
			acl:   ACL{},
			actor: Actor{User: "anyone"},
			want:  PermRead, allow: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.acl.Allows(c.actor, c.want); got != c.allow {
				t.Fatalf("Allows(%+v, %b) = %v, want %v", c.actor, c.want, got, c.allow)
			}
		})
	}
}

func TestACL_ResolveInherit(t *testing.T) {
	parent := ACL{Owner: "alice", OwnerPerms: PermRead | PermWrite}
	child := ACL{Inherit: true}

	if !child.Resolve(parent).Allows(Actor{User: "alice"}, PermWrite) {
		t.Errorf("inheriting child should grant the parent's owner write")
	}

	own := ACL{Owner: "bob", OwnerPerms: PermRead}
	if own.Resolve(parent).Allows(Actor{User: "alice"}, PermWrite) {
		t.Errorf("a non-inheriting ACL must not adopt the parent")
	}
}

func TestACLSet_Effective_KeysOnFieldID(t *testing.T) {
	objectDefault := ACL{Owner: "alice", OwnerPerms: PermRead | PermWrite}
	set := ACLSet{
		"cic:compute:vm:memory": ACL{Inherit: true},                             // inherits object default
		"cic:compute:vm:shape":  ACL{Owner: "bob", OwnerPerms: PermRead},         // own ACL
	}

	// inheriting field → object default applies
	if !set.Effective("cic:compute:vm:memory", objectDefault).Allows(Actor{User: "alice"}, PermWrite) {
		t.Errorf("inheriting field should grant alice write via the object default")
	}
	// own ACL field → its own owner, read-only
	shape := set.Effective("cic:compute:vm:shape", objectDefault)
	if !shape.Allows(Actor{User: "bob"}, PermRead) || shape.Allows(Actor{User: "bob"}, PermWrite) {
		t.Errorf("shape ACL should grant bob read but not write")
	}
	// unknown field → object default
	if !set.Effective("cic:compute:vm:unknown", objectDefault).Allows(Actor{User: "alice"}, PermRead) {
		t.Errorf("unknown field should fall back to the object default")
	}
}
