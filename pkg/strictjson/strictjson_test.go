package strictjson

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testShape = &Shape{
	Keys: map[string]*Shape{
		"kind": nil,
		"obj":  {Keys: map[string]*Shape{"a": nil, "b": nil}, Required: []string{"a"}},
		"list": {Elem: &Shape{Keys: map[string]*Shape{"id": nil}, Required: []string{"id"}}},
		"free": {Free: true},
	},
	Required: []string{"kind"},
}

func TestScanAcceptsTheExactShape(t *testing.T) {
	for _, ok := range []string{
		`{"kind":"x"}`,
		`{"kind":"x","obj":{"a":1},"list":[{"id":"1"},{"id":"2"}],"free":{"any":1,"key":2}}`,
		`{"kind":"x","obj":{"a":1,"b":[1,2,{"x":1}]}}`, // a leaf may be any value, including nested containers
		` {"kind":"x"} `, // surrounding whitespace is not data
		`{"kind":"x","list":[]}`,
		`{"kind":"x","free":{}}`,
	} {
		assert.NoError(t, Scan([]byte(ok), testShape), ok)
	}
}

func TestScanRefusesForTheNamedReason(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{"duplicate key", `{"kind":"x","kind":"y"}`, `duplicate key "kind"`},
		{"duplicate key in a nested object", `{"kind":"x","obj":{"a":1,"a":2}}`, `$.obj: duplicate key "a"`},
		{"duplicate key in a free object", `{"kind":"x","free":{"k":1,"k":2}}`, `$.free: duplicate key "k"`},
		{"unknown key", `{"kind":"x","extra":1}`, `unknown or differently spelled key "extra"`},
		{"case-variant key", `{"Kind":"x"}`, `unknown or differently spelled key "Kind"`},
		{"unknown key in a nested object", `{"kind":"x","obj":{"a":1,"c":2}}`, `$.obj: unknown`},
		{"unknown key in an array element", `{"kind":"x","list":[{"id":"1","z":1}]}`, `$.list[0]: unknown`},
		{"missing required key", `{}`, `required key "kind" is missing`},
		{"missing required key in a nested object", `{"kind":"x","obj":{"b":1}}`, `$.obj: required key "a" is missing`},
		{"missing required key in an array element", `{"kind":"x","list":[{"id":"1"},{}]}`, `$.list[1]: required key "id" is missing`},
		{"trailing data", `{"kind":"x"} {}`, "unexpected data after the JSON value"},
		{"trailing garbage", `{"kind":"x"}x`, "unexpected data after the JSON value"},
		{"not an object", `[]`, "$: an object is required"},
		{"an object where an array is required", `{"kind":"x","list":{}}`, "$.list: an array is required"},
		{"an array where an object is required", `{"kind":"x","obj":[]}`, "$.obj: an object is required"},
		{"empty input", ``, "EOF"},
		{"truncated", `{"kind":"x"`, "EOF"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Scan([]byte(c.raw), testShape)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

func TestAnOffendingKeyIsClippedInTheMessage(t *testing.T) {
	long := strings.Repeat("k", 200)
	err := Scan([]byte(`{"`+long+`":1}`), testShape)
	require.Error(t, err)
	assert.Contains(t, err.Error(), strings.Repeat("k", maxKeyShown)+"...")
	assert.NotContains(t, err.Error(), strings.Repeat("k", maxKeyShown+1))
	assert.Equal(t, long[:maxKeyShown], clip(long[:maxKeyShown]), "a key at the limit is shown whole")
	assert.Equal(t, long[:maxKeyShown]+"...", clip(long[:maxKeyShown+1]))
}

func TestANilShapeIsAnyOneValue(t *testing.T) {
	require.NoError(t, Scan([]byte(`{"a":[1,{"b":2}]}`), nil))
	assert.Error(t, Scan([]byte(`1 2`), nil), "a second value is still trailing data")
}
