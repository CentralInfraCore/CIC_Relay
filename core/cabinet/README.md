# Cabinet Package (core/cabinet)

The `core/cabinet` package implements the central static registry for the CIC Relay system. It is responsible for managing and exposing schemas, plugin metadata, and execution graphs. This registry does not contain runtime state or live instances—it acts as a resolution layer and knowledge base.

## Structure Overview

### Source Files

* `cabinet.go`: Main implementation file. Contains the concrete `Cabinet` struct that fulfills the `Cabinet` interface.
* `interface.go`: Defines the `Cabinet` interface for external access and integration.
* `schema.go`: Defines the `SchemaDef` structure and schema registration logic.
* `plugin.go`: Defines `PluginDescriptor` and plugin-related logic.
* `graph.go`: Defines `ExecutionGraph` and execution flow representation.
* `types.go`: Common struct definitions and utility methods shared across components.
* `spec.md`: Human-readable specification of the Cabinet module, including purpose, structure, and interfaces.

### Test Files

* `cabinet_test.go`: Core tests for Cabinet behavior (e.g. registration, retrieval).
* `interface_test.go`: Black-box test of the Cabinet interface from an external perspective.
* `schema_test.go`: Unit tests specific to schema registration and access.
* `plugin_test.go`: Unit tests for plugin descriptor behavior.
* `graph_test.go`: Unit tests for execution graph structure and registration.
* `types_test.go`: Tests for structural correctness and helper methods (e.g. `Key()`, `Supports()`).
* `api_contract_test.go`: Optional API-level tests verifying adherence to expected interface contracts.

## Status

This package is currently under active development as part of `feature-006` in the `#CIC-DEV-FLOW-MODEL-20250522` context.

## Next Steps

* Finalize concrete method implementations in `cabinet.go`
* Extend coverage in unit and contract tests
* Integrate with plugin loader and dispatcher runtime
* Document version compatibility and signature verification mechanisms
