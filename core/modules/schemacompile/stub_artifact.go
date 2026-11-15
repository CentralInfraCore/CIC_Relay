package schemacompile

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// stubArtifact is the document BuildSchema generates in stub mode. It is serialized with a YAML
// encoder, not interpolated into a template: the encoder quotes a value whenever its plain form
// would parse as something other than a string, so a version such as "1.0" stays the string
// "1.0" (not the float 1) and a name such as "true" stays a string (not a bool). A character
// whitelist alone does not give that guarantee: "1.0", "true", "null" and "1e3" all pass it.
type stubArtifact struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	} `yaml:"metadata"`
	Spec map[string]interface{} `yaml:"spec"`
}

// renderStubArtifact returns the stub artifact document. For ordinary values the bytes equal
// what the former template produced (two-space indent, same key order).
func renderStubArtifact(schema, version string) (string, error) {
	a := stubArtifact{APIVersion: "cic.com/v1", Kind: "SchemaArtifact", Spec: map[string]interface{}{}}
	a.Metadata.Name = schema
	a.Metadata.Version = version

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(a); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}
