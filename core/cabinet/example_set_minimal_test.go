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
		WorkflowID: "wf.compile@v1",
		Modules:    []ModuleID{"modA@v1"},
		Payload:    map[string]any{"source": "int main(){}"},
	}

	// To make the example pass, we must use the constructors to create valid objects with checksums.
	wf, err := NewWorkflow(BaseBlock{Name: "wf.compile", Version: "v1"}, nil)
	if err != nil {
		fmt.Println("setup error (workflow):", err)
		return
	}
	wfStore.Put(wf)

	mod, err := NewModuleDescriptor(BaseBlock{Name: "modA", Version: "v1"}, "", nil, nil, nil)
	if err != nil {
		fmt.Println("setup error (module):", err)
		return
	}
	modStore.Put(mod)

	res, err := api.Set(context.Background(), req)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("ok", len(res.Results))
	// Output:
	// ok 0
}
