package iac

import (
	"encoding/json"
	"reflect"
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

func TestValueKind_String(t *testing.T) {
	cases := map[ValueKind]string{
		KindNull: "null", KindBool: "bool", KindNumber: "number", KindString: "string",
		KindList: "list", KindObject: "object", KindObjectRef: "object-ref", KindSecretRef: "secret-ref",
		ValueKind(99): "unknown",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("ValueKind(%d).String() = %q, want %q", uint8(k), got, want)
		}
	}
}

func TestValue_BoolAndListAccessorsAreKindGuarded(t *testing.T) {
	if b, ok := BoolValue(true).Bool(); !ok || !b {
		t.Errorf("Bool() on true = %v, %v", b, ok)
	}
	if b, ok := BoolValue(false).Bool(); !ok || b {
		t.Errorf("Bool() on false = %v, %v", b, ok)
	}
	if _, ok := StringValue("true").Bool(); ok {
		t.Errorf("Bool() must not succeed on a string")
	}

	l, ok := ListValue([]Value{StringValue("a"), NullValue()}).List()
	if !ok || len(l) != 2 || l[0].Kind() != KindString || l[1].Kind() != KindNull {
		t.Errorf("List() = %v, %v", l, ok)
	}
	if _, ok := ObjectValue(map[string]Value{}).List(); ok {
		t.Errorf("List() must not succeed on an object")
	}
	if _, ok := ListValue(nil).Object(); ok {
		t.Errorf("Object() must not succeed on a list")
	}
}

func TestNumberValue(t *testing.T) {
	for _, in := range []interface{}{4, uint8(4), int64(4), 4.0, json.Number("4.0"), "4"} {
		v, ok := NumberValue(in)
		if !ok {
			t.Errorf("NumberValue(%#v) not numeric", in)
			continue
		}
		if n, ok := v.Number(); !ok || n != "4" {
			t.Errorf("NumberValue(%#v) = %q, %v; want canonical \"4\"", in, n, ok)
		}
	}
	for _, in := range []interface{}{true, nil, "abc", "", []int{1}, struct{}{}} {
		if _, ok := NumberValue(in); ok {
			t.Errorf("NumberValue(%#v) reported numeric", in)
		}
	}
}

func TestFromRaw_NumericKindsAndFallback(t *testing.T) {
	if v := FromRaw(uint8(7)); v.Kind() != KindNumber {
		t.Errorf("uint8 -> %s, want number", v.Kind())
	} else if n, _ := v.Number(); n != "7" {
		t.Errorf("uint8(7) number = %q", n)
	}
	// A type with no JSON meaning is kept as its printed form, not dropped.
	v := FromRaw(struct{ A int }{A: 1})
	if s, ok := v.Str(); v.Kind() != KindString || !ok || s != "{1}" {
		t.Errorf("fallback = kind %s, %q", v.Kind(), s)
	}
}

func TestValue_Raw(t *testing.T) {
	cases := []struct {
		name string
		in   Value
		want interface{}
	}{
		{"null", NullValue(), nil},
		{"bool", BoolValue(true), true},
		{"string", StringValue("web-01"), "web-01"},
		{"number is canonical json.Number", FromRaw(4.0), json.Number("4")},
		{"list", FromRaw([]interface{}{"a", 1}), []interface{}{"a", json.Number("1")}},
		{"object", FromRaw(map[string]interface{}{"k": "v"}), map[string]interface{}{"k": "v"}},
		{"object ref with kind", ObjectRefValue(FieldRef{Target: "subnet-a", Kind: "Subnet"}),
			map[string]interface{}{"ref": "subnet-a", "kind": "Subnet"}},
		{"object ref without kind omits the key", ObjectRefValue(FieldRef{Target: "subnet-a"}),
			map[string]interface{}{"ref": "subnet-a"}},
		{"secret ref", SecretRefValue(SecretRef{Path: "/vault/db/pw"}),
			map[string]interface{}{"secret_ref": "/vault/db/pw"}},
		{"zero Value is null", Value{}, nil},
		{"unknown kind degrades to nil", Value{kind: ValueKind(99)}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.Raw(); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Raw() = %#v, want %#v", got, c.want)
			}
		})
	}
}

// Raw() and FromRaw() are inverses for the reference kinds: a ref must not decay
// into an ordinary object (and a secret ref must not become one) on the way back.
func TestValue_RawFromRawKeepsReferenceKinds(t *testing.T) {
	for _, in := range []Value{
		ObjectRefValue(FieldRef{Target: "subnet-a", Kind: "Subnet"}),
		ObjectRefValue(FieldRef{Target: "subnet-a"}),
		SecretRefValue(SecretRef{Path: "/p"}),
	} {
		back := FromRaw(in.Raw())
		if back.Kind() != in.Kind() || !reflect.DeepEqual(back.Raw(), in.Raw()) {
			t.Errorf("round trip changed %s %#v -> %s %#v", in.Kind(), in.Raw(), back.Kind(), back.Raw())
		}
	}
}

func TestValue_Digest_KindsAreDistinct(t *testing.T) {
	str, err1 := StringValue("4").Digest()
	num, err2 := FromRaw(4).Digest()
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if str == num {
		t.Errorf("the string \"4\" and the number 4 must not share a digest")
	}
	if d, err := NullValue().Digest(); err != nil || len(d) != len("sha256:")+64 {
		t.Errorf("null digest = %q, %v", d, err)
	}
}

func TestValue_Digest_InvalidNumberIsAnError(t *testing.T) {
	// A number token that is not valid JSON must surface as an error, never a panic
	// and never a digest over garbage.
	if d, err := (Value{kind: KindNumber, number: "NaN"}).Digest(); err == nil {
		t.Errorf("digest of an invalid number token = %q, want an error", d)
	}
}
