package iac

import (
	"encoding/json"
	"testing"
)

func TestFromRaw_KindsAndRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		raw  interface{}
		kind ValueKind
	}{
		{"null", nil, KindNull},
		{"bool", true, KindBool},
		{"number int", 4, KindNumber},
		{"number float", 4.0, KindNumber},
		{"string", "web-01", KindString},
		{"list", []interface{}{"a", 1}, KindList},
		{"object", map[string]interface{}{"k": "v"}, KindObject},
		{"object ref", map[string]interface{}{"ref": "subnet-a", "kind": "Subnet"}, KindObjectRef},
		{"secret ref", map[string]interface{}{"secret_ref": "/vault/db/pw"}, KindSecretRef},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := FromRaw(c.raw)
			if v.Kind() != c.kind {
				t.Fatalf("kind = %s, want %s", v.Kind(), c.kind)
			}
		})
	}
}

func TestValue_AccessorsAreKindGuarded(t *testing.T) {
	s := StringValue("x")
	if _, ok := s.Number(); ok {
		t.Errorf("Number() must not succeed on a string")
	}
	if got, ok := s.Str(); !ok || got != "x" {
		t.Errorf("Str() = %q, %v", got, ok)
	}
	if r, ok := FromRaw(map[string]interface{}{"ref": "subnet-a", "kind": "Subnet"}).Ref(); !ok || r.Target != "subnet-a" || r.Kind != "Subnet" {
		t.Errorf("Ref() = %+v, %v", r, ok)
	}
	if sec, ok := FromRaw(map[string]interface{}{"secret_ref": "/p"}).Secret(); !ok || sec.Path != "/p" {
		t.Errorf("Secret() = %+v, %v", sec, ok)
	}
}

func TestValue_AbsentDistinctFromNull(t *testing.T) {
	withNull, err := ObjectValue(map[string]Value{"id": NullValue()}).Digest()
	if err != nil {
		t.Fatal(err)
	}
	absent, err := ObjectValue(map[string]Value{}).Digest()
	if err != nil {
		t.Fatal(err)
	}
	if withNull == absent {
		t.Errorf("an explicit null key must digest differently from an absent key")
	}
	// and the null key is present with kind null
	obj, _ := ObjectValue(map[string]Value{"id": NullValue()}).Object()
	if v, ok := obj["id"]; !ok || v.Kind() != KindNull {
		t.Errorf("null key should be present with kind null")
	}
}

func TestValue_Digest_Deterministic(t *testing.T) {
	// map key order must not matter (built from two different raw orderings)
	a := FromRaw(map[string]interface{}{"z": 1, "a": 2, "m": 3})
	b := FromRaw(map[string]interface{}{"a": 2, "m": 3, "z": 1})
	da, _ := a.Digest()
	db, _ := b.Digest()
	if da != db {
		t.Errorf("digest depends on key order: %s vs %s", da, db)
	}
	// number representation must not matter
	n1, _ := FromRaw(4).Digest()
	n2, _ := FromRaw(4.0).Digest()
	n3, _ := FromRaw(json.Number("4e0")).Digest()
	if n1 != n2 || n2 != n3 {
		t.Errorf("number representation changed the digest: %s / %s / %s", n1, n2, n3)
	}
}

func TestNode_Validate_SecretTypeBoundary(t *testing.T) {
	secretMeta := Meta{Sensitive: SensitivePolicy{Class: SensitiveSecret}}
	noneMeta := Meta{Sensitive: SensitivePolicy{Class: SensitiveNone}}

	// secret field holding a secret ref → ok
	if err := (Node{Value: SecretRefValue(SecretRef{Path: "/p"}), Meta: secretMeta}).Validate(); err != nil {
		t.Errorf("secret + secret ref should be valid: %v", err)
	}
	// secret field holding plaintext → rejected
	if err := (Node{Value: StringValue("hunter2"), Meta: secretMeta}).Validate(); err == nil {
		t.Errorf("secret + plaintext must be rejected")
	}
	// non-secret field holding a secret ref → rejected
	if err := (Node{Value: SecretRefValue(SecretRef{Path: "/p"}), Meta: noneMeta}).Validate(); err == nil {
		t.Errorf("non-secret + secret ref must be rejected")
	}
	// ordinary value → ok
	if err := (Node{Value: StringValue("public"), Meta: noneMeta}).Validate(); err != nil {
		t.Errorf("non-secret + plaintext should be valid: %v", err)
	}
}
