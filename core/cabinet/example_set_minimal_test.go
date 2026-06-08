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
	// Add a default WasmManagerConfig for the example
	wasmConfig := WasmManagerConfig{LRUCacheSize: 10, DefaultTimeoutSeconds: 1}
	svc, err := NewCabinetService(context.Background(), schemaStore, modStore, wfStore, wasmConfig)
	if err != nil {
		fmt.Println("setup error (cabinet service):", err)
		return
	}

	// Create the API instance using the NewAPI constructor
	api := NewAPI(svc)

	const (
		moduleID   = "core.test@1.0" // Use our native test module
		workflowID = "wf.echo@1.0"
	)

	initialPayload := map[string]any{"source": "hello from workflow"}
	req := &SetPayload{
		WorkflowID: workflowID,
		Modules:    []ModuleID{moduleID},
		Payload:    initialPayload,
	}

	// To make the example pass, we must use the constructors to create valid objects with checksums.
	// Register a dummy schema, though not directly used by core.test.echo
	schema, err := NewSchema(context.Background(), SchemaIdentity{Version: "1.0"}, SchemaConfig{Name: "dummy.schema"}, SchemaSigning{})
	if err != nil {
		fmt.Println("setup error (schema):", err)
		return
	}
	schemaStore.Put(schema)

	// Register the core.test native module
	testFuncs := make(NativeFunctions)
	testFuncs["echo"] = CoreEcho // Our native echo function

	coreTestMeta := BaseBlock{Name: "core.test", Version: "1.0", Description: "A native module for testing basic functionality."}
	coreTestModule, err := NewModuleDescriptor(context.Background(), coreTestMeta, "native/test", nil, nil, nil)
	if err != nil {
		fmt.Println("setup error (core.test module descriptor):", err)
		return
	}
	coreTestModule.NativeImpl = testFuncs // Attach the native function map
	modStore.Put(coreTestModule)

	// Create a workflow that uses the core.test.echo function with the full module ID
	wf, err := NewWorkflow(context.Background(), BaseBlock{Name: "wf.echo", Version: "1.0"}, []string{"core.test@1.0.echo(input)"})
	if err != nil {
		fmt.Println("setup error (workflow):", err)
		return
	}
	wfStore.Put(wf)

	res, err := api.Set(context.Background(), req)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	// The result should contain the echoed input data under the "result" key
	fmt.Printf("ok %v\n", res.Results["result"])
	// Output:
	// ok map[source:hello from workflow]
}
