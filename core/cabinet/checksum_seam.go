package cabinet

// seam for schema-specific checksum that tests can override.
type checksumSchemaFn = func(*BaseBlock, SchemaDef) (string, error)

// calculateChecksum is the schema checksum function used by Schema constructors and methods.
// Tests can override this variable to inject a fake/deterministic implementation.
var calculateChecksum checksumSchemaFn = func(meta *BaseBlock, spec SchemaDef) (string, error) {
	return genericChecksum(meta, spec)
}
