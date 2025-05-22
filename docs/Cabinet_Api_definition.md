# Cabinet API Definition

This document defines the core API interface of the `Cabinet` module within the CIC Relay system.

## Purpose

The Cabinet API exposes methods to register and retrieve schemas, plugin descriptors, and execution graphs. It serves as the static registry interface for the Relay's execution and validation logic.

## Interface: `Cabinet`

```go
// Cabinet is the central static registry for schemas, plugins, and graphs.
type Cabinet interface {
    // Schema-related methods
    RegisterSchema(schema *SchemaDef) error
    GetSchema(ref string) (*SchemaDef, error)
    
    // Plugin-related methods
    RegisterPlugin(desc *PluginDescriptor) error
    GetValidator(schemaRef string) (*PluginDescriptor, error)
    GetPlugin(pluginID string) (*PluginDescriptor, error)

    // Graph-related methods
    RegisterGraph(graph *ExecutionGraph) error
    GetGraph(graphID string) (*ExecutionGraph, error)

    // Intent resolution
    ResolveIntent(graphRoot string, payload map[string]interface{}) (*ExecutionPlan, error)

    // Utility
    ListSchemas() []string
    ListPlugins() []string
    ListGraphs() []string
}
```

## Supporting Types (Reference)

```go
type SchemaDef struct {
    Name     string
    Version  string
    Fields   map[string]FieldSpec
    Sign     string
}

type PluginDescriptor struct {
    ID        string
    Type      string // validator, executor, etc.
    Path      string
    Symbol    string
    SchemaRef string
    Sign      string
}

type ExecutionGraph struct {
    ID       string
    Nodes    map[string]GraphNode
    Edges    map[string][]string
    Bindings map[string]PluginBinding
}

type ExecutionPlan struct {
    GraphID     string
    OrderedNodes []string // node IDs in execution order
    Steps        []ExecutionStep
}

type ExecutionStep struct {
    NodeID     string
    PluginID   string
    SchemaRef  string
}
```

## Notes

* All methods must be concurrency-safe.
* The API allows modular replacement of the Cabinet implementation (e.g. mock, memory, or future persistent version).
* Errors returned should use standardized error types (e.g. `ErrSchemaNotFound`, `ErrInvalidPlugin`) for consistent handling.

## Implementation Package

* Package: `relay/cabinet`
* Interface file: `interface.go`
* Concrete implementation: `cabinet.go`
