package cabinet

import (
	"context"
	"testing"
)

// TestDomainConstructors_GoldenChecksums verifies that the New... constructors
// for all domain types correctly calculate and assign the deterministic checksum
// based on the central validateAndChecksum function.
func TestDomainConstructors_GoldenChecksums(t *testing.T) {
	t.Run("Schema_GoldenChecksum", func(t *testing.T) {
		identity := SchemaIdentity{Version: "1.0"}
		config := SchemaConfig{Name: "golden-schema", Dependencies: []string{"dep1"}}

		// Checksum covers: KindSpec prefix + canonical_json(metaForChecksum) + canonical_json(SchemaConfig).
		const goldenSchemaChecksum = "7ce0e3ec09d09d9cb71ce4bbd0a2954dd4c9eb5faf48e3c739fd2a8dc565aaa5"

		schema, err := NewSchema(context.Background(), identity, config, SchemaSigning{})
		if err != nil {
			t.Fatalf("NewSchema() failed: %v", err)
		}

		if schema.Checksum() != goldenSchemaChecksum {
			t.Errorf("Schema checksum mismatch:\n- Got:  %s\n- Want: %s", schema.Checksum(), goldenSchemaChecksum)
		}
	})

	t.Run("Workflow_GoldenChecksum", func(t *testing.T) {
		identity := WorkflowIdentity{Version: "1.0"}
		config := WorkflowConfig{Name: "golden-wf", Steps: []string{"step1", "step2"}}

		// Checksum covers: KindWf prefix + canonical_json(metaForChecksum) + canonical_json(WorkflowConfig).
		const goldenWorkflowChecksum = "4721d29a743f3a18ef57455f9aa5c692468f68afe96cdef11387587a365bfb20"

		wf, err := NewWorkflow(context.Background(), identity, config, WorkflowSigning{})
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
		const goldenModuleChecksum = "7ad9f16c6ac3cd6997954a252b4feeec2aa65ec02df0dbd64287edf06be1f46e"

		mod, err := NewModuleDescriptor(context.Background(), meta, "executor", []string{"*"}, nil, nil)
		if err != nil {
			t.Fatalf("NewModuleDescriptor() failed: %v", err)
		}

		if mod.Checksum() != goldenModuleChecksum {
			t.Errorf("ModuleDescriptor checksum mismatch:\n- Got:  %s\n- Want: %s", mod.Checksum(), goldenModuleChecksum)
		}
	})
}
