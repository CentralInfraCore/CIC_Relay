package cabinet

import (
	"context"
	"fmt"
)

func ExampleAPI_Set_minimal() {
	// Setup: create in-memory stores for workflows and modules
	wfStore := NewMemWorkflowStore()
	modStore := NewMemModuleStore()
	schemaStore := NewMemSchemaStore()

	// Create a CabinetService with the in-memory stores
	svc := NewCabinetService(schemaStore, modStore, wfStore)

	// Create the API instance using the NewAPI constructor
	api := NewAPI(svc)

	req := &SetPayload{
		WorkflowID: "wf.compile@v1",       // Corrected to match ID() format
		Modules:    []ModuleID{"modA@v1"}, // Corrected to match ID() format
		Payload:    map[string]any{"source": "int main(){}"},
	}

	// To make the example pass, we need to ensure "wf.compile" and "modA" exist in the stores
	// For a minimal example, we can put them directly into the stores
	wfStore.Put(&Workflow{Meta: BaseBlock{Name: "wf.compile", Version: "v1"}})
	modStore.Put(&ModuleDescriptor{Meta: BaseBlock{Name: "modA", Version: "v1"}})

	res, err := api.Set(context.Background(), req)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("ok", len(res.Results))
	// Output:
	// ok 0
}
