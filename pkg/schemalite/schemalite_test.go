package schemalite

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustCompile(t *testing.T, s string) *Schema {
	t.Helper()
	sc, err := Compile([]byte(s))
	require.NoError(t, err, s)
	return sc
}

func TestValidAndInvalidPayloads(t *testing.T) {
	sc := mustCompile(t, `{"$schema":"x","title":"t","description":"d","type":"object","required":["id","kind"],"additionalProperties":false,
		"properties":{
		 "id":{"type":"string","minLength":2,"maxLength":8,"pattern":"^[a-z]+$"},
		 "kind":{"enum":["read","write",3,null,true]},
		 "n":{"type":"integer","minimum":1,"maximum":10},
		 "x":{"type":"number"},
		 "tags":{"type":"array","minItems":1,"maxItems":2,"items":{"type":"string"}},
		 "c":{"const":"fixed"},
		 "o":{"type":"object","properties":{"z":{"type":"boolean"}}}}}`)

	for _, ok := range []string{
		`{"id":"ab","kind":"read"}`,
		`{"id":"abcdefgh","kind":"write","n":1,"x":1.5,"tags":["a"],"c":"fixed","o":{"z":true}}`,
		`{"id":"ab","kind":3,"n":10,"x":2}`, // an integer is also a number
		`{"id":"ab","kind":null}`,
		`{"id":"ab","kind":true,"tags":["a","b"]}`,
		`{"id":"ab","kind":"read","n":1.0}`,         // 1.0 is an integer value
		`{"id":"ab","kind":"read","o":{"other":1}}`, // additionalProperties is only closed where it says so
		` {"id":"ab","kind":"read"} `,
	} {
		assert.NoError(t, sc.Validate([]byte(ok)), ok)
	}
	for name, bad := range map[string]string{
		"wrong type at the root": `[]`,
		"missing required":       `{"id":"ab"}`,
		"extra property":         `{"id":"ab","kind":"read","extra":1}`,
		"string too short":       `{"id":"a","kind":"read"}`,
		"string too long":        `{"id":"abcdefghi","kind":"read"}`,
		"pattern":                `{"id":"AB","kind":"read"}`,
		"enum":                   `{"id":"ab","kind":"admin"}`,
		"enum number":            `{"id":"ab","kind":4}`,
		"integer is a float":     `{"id":"ab","kind":"read","n":1.5}`,
		"integer below minimum":  `{"id":"ab","kind":"read","n":0}`,
		"integer above maximum":  `{"id":"ab","kind":"read","n":11}`,
		"number is a string":     `{"id":"ab","kind":"read","x":"1"}`,
		"too few items":          `{"id":"ab","kind":"read","tags":[]}`,
		"too many items":         `{"id":"ab","kind":"read","tags":["a","b","c"]}`,
		"item of the wrong type": `{"id":"ab","kind":"read","tags":[1]}`,
		"const":                  `{"id":"ab","kind":"read","c":"other"}`,
		"nested type":            `{"id":"ab","kind":"read","o":{"z":"yes"}}`,
		"null where a string":    `{"id":"ab","kind":"read","c":null}`,
		"trailing data":          `{"id":"ab","kind":"read"} {}`,
		"not json":               `nope`,
		"empty":                  ``,
	} {
		t.Run(name, func(t *testing.T) { assert.Error(t, sc.Validate([]byte(bad)), bad) })
	}
}

func TestBoundariesAreInclusive(t *testing.T) {
	sc := mustCompile(t, `{"type":"object","properties":{"s":{"type":"string","minLength":2,"maxLength":3},"n":{"minimum":5,"maximum":6},"a":{"type":"array","minItems":1,"maxItems":1}}}`)
	for _, ok := range []string{`{"s":"ab"}`, `{"s":"abc"}`, `{"n":5}`, `{"n":6}`, `{"a":[1]}`, `{"s":"éé"}`} {
		assert.NoError(t, sc.Validate([]byte(ok)), ok)
	}
	for _, bad := range []string{`{"s":"a"}`, `{"s":"abcd"}`, `{"n":4.99}`, `{"n":6.01}`, `{"a":[]}`, `{"a":[1,2]}`} {
		assert.Error(t, sc.Validate([]byte(bad)), bad)
	}
}

func TestTheSubsetIsClosed(t *testing.T) {
	for _, bad := range []string{
		`{"$ref":"#/x"}`, `{"oneOf":[]}`, `{"allOf":[]}`, `{"anyOf":[]}`, `{"not":{}}`, `{"format":"date"}`, `{"patternProperties":{}}`,
		`{"if":{}}`, `{"uniqueItems":true}`, `{"multipleOf":2}`, `{"exclusiveMinimum":1}`, `{"definitions":{}}`,
		`{"properties":{"a":{"$ref":"x"}}}`, `{"items":{"unknown":1}}`,
	} {
		_, err := Compile([]byte(bad))
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "not supported", bad)
	}
}

func TestMalformedSchemasAreRefused(t *testing.T) {
	for _, bad := range []string{
		``, `nope`, `[]`, `"s"`, `{} {}`,
		`{"type":"thing"}`, `{"type":7}`, `{"type":["string"]}`,
		`{"properties":[]}`, `{"properties":{"a":1}}`,
		`{"required":"a"}`, `{"required":[1]}`,
		`{"additionalProperties":{}}`, `{"additionalProperties":"no"}`,
		`{"items":[]}`, `{"enum":[]}`, `{"enum":"a"}`, `{"enum":[{}]}`, `{"enum":[[1]]}`, `{"const":{}}`, `{"const":[1]}`,
		`{"minLength":-1}`, `{"maxLength":1.5}`, `{"minItems":"1"}`, `{"maxItems":-2}`, `{"minLength":99999999999}`,
		`{"minimum":"1"}`, `{"maximum":true}`, `{"pattern":1}`, `{"pattern":"("}`,
	} {
		_, err := Compile([]byte(bad))
		assert.Error(t, err, bad)
	}
}

func TestBooleanAndNullTypes(t *testing.T) {
	assert.NoError(t, mustCompile(t, `{"type":"boolean"}`).Validate([]byte(`true`)))
	assert.Error(t, mustCompile(t, `{"type":"boolean"}`).Validate([]byte(`1`)))
	assert.NoError(t, mustCompile(t, `{"type":"null"}`).Validate([]byte(`null`)))
	assert.Error(t, mustCompile(t, `{"type":"null"}`).Validate([]byte(`0`)))
	assert.NoError(t, mustCompile(t, `{"type":"string"}`).Validate([]byte(`"x"`)))
	assert.Error(t, mustCompile(t, `{"type":"string"}`).Validate([]byte(`null`)))
	assert.NoError(t, mustCompile(t, `{"type":"array"}`).Validate([]byte(`[]`)))
	assert.Error(t, mustCompile(t, `{"type":"array"}`).Validate([]byte(`{}`)))
	assert.NoError(t, mustCompile(t, `{"type":"object"}`).Validate([]byte(`{}`)))
	assert.Error(t, mustCompile(t, `{"type":"object"}`).Validate([]byte(`[]`)))
	assert.NoError(t, mustCompile(t, `{}`).Validate([]byte(`{"anything":[1,"x",null]}`)), "an empty schema accepts any value")
}

func TestNumbersAndEnumsCompareByValue(t *testing.T) {
	sc := mustCompile(t, `{"enum":[1, "a"]}`)
	assert.NoError(t, sc.Validate([]byte(`1.0`)))
	assert.NoError(t, sc.Validate([]byte(`1e0`)))
	assert.Error(t, sc.Validate([]byte(`"1"`)), "a string is not the number")
	assert.Error(t, sc.Validate([]byte(`2`)))
	c := mustCompile(t, `{"const":1}`)
	assert.Error(t, c.Validate([]byte(`"a"`)), "a different kind never equals a number")
	assert.NoError(t, mustCompile(t, `{"const":"a"}`).Validate([]byte(`"a"`)))
	assert.Error(t, mustCompile(t, `{"const":"a"}`).Validate([]byte(`1`)))
	assert.NoError(t, mustCompile(t, `{"const":null}`).Validate([]byte(`null`)))
	assert.Error(t, mustCompile(t, `{"enum":[1]}`).Validate([]byte(`1.5`)))
}

func TestAnIntegerIsANumberButANumberIsNotAlwaysAnInteger(t *testing.T) {
	assert.NoError(t, mustCompile(t, `{"type":"number"}`).Validate([]byte(`3`)))
	assert.NoError(t, mustCompile(t, `{"type":"number"}`).Validate([]byte(`3.5`)))
	assert.NoError(t, mustCompile(t, `{"type":"integer"}`).Validate([]byte(`3`)))
	assert.Error(t, mustCompile(t, `{"type":"integer"}`).Validate([]byte(`3.5`)))
	assert.Error(t, mustCompile(t, `{"type":"number"}`).Validate([]byte(`"3"`)))
}

func TestHugeNumbersDoNotCrashTheValidator(t *testing.T) {
	sc := mustCompile(t, `{"type":"integer","maximum":10}`)
	assert.Error(t, sc.Validate([]byte(`1e999`)), "an out-of-range number is refused, not crashed on")
	assert.Error(t, sc.Validate([]byte(`1000000000000000000000`)))
}

func TestSizeKeywordBoundsAreInclusive(t *testing.T) {
	for _, ok := range []string{`{"minLength":0}`, `{"maxItems":0}`, `{"minLength":2147483647}`} {
		_, err := Compile([]byte(ok))
		assert.NoError(t, err, ok)
	}
	_, err := Compile([]byte(`{"minLength":2147483648}`))
	assert.Error(t, err)
	assert.Error(t, mustCompile(t, `{"maxItems":0}`).Validate([]byte(`[1]`)))
	assert.NoError(t, mustCompile(t, `{"maxItems":0}`).Validate([]byte(`[]`)))
}
