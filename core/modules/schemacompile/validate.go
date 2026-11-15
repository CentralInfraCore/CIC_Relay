package schemacompile

import (
	"fmt"
	"regexp"
)

var (
	// schemaNamePattern: a schema name such as "postgresql" or "some.schema".
	schemaNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	// versionPattern: a version such as "v1.0.0" or "v0.18.dev".
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
)

// ValidateSchemaRef checks the caller-supplied schema name and version against a strict
// format. They are interpolated into a YAML document that the relay then signs, so a
// newline, colon, space or any other character outside the format could inject keys into
// the signed artifact. It rejects them before they reach any formatted string.
func ValidateSchemaRef(schema, version string) error {
	if !schemaNamePattern.MatchString(schema) {
		return fmt.Errorf("invalid schema name %q: must match %s", schema, schemaNamePattern)
	}
	if !versionPattern.MatchString(version) {
		return fmt.Errorf("invalid version %q: must match %s", version, versionPattern)
	}
	return nil
}
