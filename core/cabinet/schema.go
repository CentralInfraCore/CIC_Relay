package cabinet

import "context"

// NewSchema constructs a ManagedEntity-conformant Schema with validated checksum.
func NewSchema(ctx context.Context, identity SchemaIdentity, config SchemaConfig, signing SchemaSigning) (*Schema, error) {
	sum, err := validateAndChecksumSchema(ctx, identity, config, signing)
	if err != nil {
		return nil, err
	}
	signing.Checksum = sum

	return &Schema{
		Identity: identity,
		Config:   config,
		Signing:  signing,
		State:    SchemaState{Valid: true},
	}, nil
}

// ID returns the unique schema identifier in the form "name@version".
// This is used for lookups in the schema store.
func (s *Schema) ID() string {
	return s.Config.Name + "@" + s.Identity.Version
}

// GetData returns the schema's config surface for checksum purposes.
func (s *Schema) GetData() any {
	return s.Config
}

// Checksum returns the pre-computed checksum from the Signing block.
func (s *Schema) Checksum() string {
	return s.Signing.Checksum
}
