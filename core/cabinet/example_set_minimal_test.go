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

	const (
		moduleID   = "modA@v1"
		workflowID = "wf.compile@v1"
	)

	req := &SetPayload{
		WorkflowID: workflowID,
		Modules:    []ModuleID{moduleID},
		Payload:    map[string]any{"source": "int main(){}"},
	}

	// The schema is used by the module, so we must create it first.
	schema, err := NewSchema(context.Background(), BaseBlock{Name: "source.code", Version: "v1"}, SchemaDef{})
	if err != nil {
		fmt.Println("setup error (schema):", err)
		return
	}
	schemaStore.Put(schema)

	// The workflow step is updated to the new command format: "component.function(data)".
	wf, err := NewWorkflow(context.Background(), BaseBlock{Name: "wf.compile", Version: "v1"}, []string{"modA.compile(source)"})
	if err != nil {
		fmt.Println("setup error (workflow):", err)
		return
	}
	wfStore.Put(wf)

	// The 'accepts' parameter is re-introduced to fix the build error.
	mod, err := NewModuleDescriptor(context.Background(), BaseBlock{Name: "modA", Version: "v1"}, "", []string{schema.ID()}, nil, nil)
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
