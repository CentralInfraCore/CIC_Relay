package cabinet

import "testing"

// TestDomainConstructors_GoldenChecksums verifies that the New... constructors
// for all domain types correctly calculate and assign the deterministic checksum
// based on the central validateAndChecksum function.
func TestDomainConstructors_GoldenChecksums(t *testing.T) {
	t.Run("Schema_GoldenChecksum", func(t *testing.T) {
		meta := BaseBlock{Name: "golden-schema", Version: "1.0"}
		spec := SchemaDef{Dependencies: []string{"dep1"}}

		// This checksum is updated based on the actual output from the corrected computeChecksum logic.
		const goldenSchemaChecksum = "9a3c6f45df100e63e0d9b692f9df871de3749e5b940f4074ee51c4f22be5f611"

		schema, err := NewSchema(meta, spec)
		if err != nil {
			t.Fatalf("NewSchema() failed: %v", err)
		}

		if schema.Checksum() != goldenSchemaChecksum {
			t.Errorf("Schema checksum mismatch:\n- Got:  %s\n- Want: %s", schema.Checksum(), goldenSchemaChecksum)
		}
	})

	t.Run("Workflow_GoldenChecksum", func(t *testing.T) {
		meta := BaseBlock{Name: "golden-wf", Version: "1.0"}
		steps := []string{"step1", "step2"}

		// This checksum is updated based on the actual output from the corrected computeChecksum logic.
		const goldenWorkflowChecksum = "ae4f2fa9f7df1b506177b1475979f7366285c3386dde7766a53e38e2aa135efb"

		wf, err := NewWorkflow(meta, steps)
		if err != nil {
			t.Fatalf("NewWorkflow() failed: %v", err)
		}

		if wf.Checksum() != goldenWorkflowChecksum {
			t.Errorf("Workflow checksum mismatch:\n- Got:  %s\n- Want: %s", wf.Checksum(), goldenWorkflowChecksum)
		}
	})

	t.Run("ModuleDescriptor_GoldenChecksum", func(t *testing.T) {
		meta := BaseBlock{Name: "golden-mod", Version: "1.0"}

		// This checksum is updated based on the actual output from the corrected computeChecksum logic.
		const goldenModuleChecksum = "b1f93d27257a66790ed3d51594062952832ed6f41198ae203cd2a0106e442d74"

		mod, err := NewModuleDescriptor(meta, "executor", []string{"*"}, nil, nil)
		if err != nil {
			t.Fatalf("NewModuleDescriptor() failed: %v", err)
		}

		if mod.Checksum() != goldenModuleChecksum {
			t.Errorf("ModuleDescriptor checksum mismatch:\n- Got:  %s\n- Want: %s", mod.Checksum(), goldenModuleChecksum)
		}
	})
}
