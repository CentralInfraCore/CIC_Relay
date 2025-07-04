# Cabinet Specification

## Purpose

The `Cabinet` object serves as the **central static knowledge base** of the CIC Relay system. It provides introspection and resolution services for schemas, plugin descriptors, and execution graphs. It is strictly **non-executing** and maintains no runtime state.

## Responsibilities

* Register and store versioned schema definitions
* Maintain metadata about available plugin modules (`.so`)
* Describe and resolve execution graphs
* Provide mappings and associations between schemas, plugins, and execution flows
* Enable graph intent resolution

## Design Principles

* The Cabinet is a **purely static registry** — no execution, no instance lifecycle management
* Entries are **referential** and **versioned**
* Plugins are referenced through descriptors, not live instances
* All plugin types (validator, executor, transformer) are handled uniformly

## Core Structures

### 1. `SchemaDef`

* `name`: human-readable identifier
* `version`: semantic version string
* `fields`: list of named and typed field specs (optional)
* `sign`: cryptographic hash or signature of the schema definition

### 2. `PluginDescriptor`

* `id`: unique plugin identifier
* `type`: validator, executor, etc.
* `path`: file path to `.so` or location descriptor
* `symbol`: name of the exported symbol to be used (usually `Register`)
* `schemaRef`: name\@version that this plugin supports or depends on

### 3. `ExecutionGraph`

* `id`: unique identifier for the graph root
* `nodes`: named steps or logic units
* `edges`: links between nodes
* `bindings`: node-to-plugin/schema references

## Functional Interfaces

* `RegisterSchema(schema: SchemaDef)`
* `RegisterPlugin(desc: PluginDescriptor)`
* `RegisterGraph(graph: ExecutionGraph)`
* `ResolveIntent(graphRoot: string, payload: map[string]any) -> ExecutionPlan`
* `GetSchema(ref: string) -> SchemaDef`
* `GetValidator(ref: string) -> PluginDescriptor`
* `GetGraph(id: string) -> ExecutionGraph`

## Plugin Integration

Each `.so` file loaded by the relay must export at minimum a `Register()` function returning a structure compatible with the expected plugin type. Plugins are discovered and registered via descriptors, **not loaded eagerly**.

## Signature Verification (Optional)

* Each schema and plugin descriptor can optionally include a `sign` field
* This can be a `sha256:` hash or GPG signature
* The relay can (optionally) validate these signatures at load-time

## Runtime Behavior

* Cabinet **does not cache** plugin instances or validator results
* It may provide in-memory indexes or mappings for faster lookup
* It is thread-safe and supports concurrent resolution requests

## Relationship to Other Modules

* The Cabinet is accessed by the **execution dispatcher**, **validator engine**, and **plugin loader**
* It is **owned by the Relay core**, but can be serialized/exported for introspection tools (e.g. CLI)

## Location

* The Cabinet lives in its own Go package: `relay/cabinet`
* Specification file: `relay/cabinet/spec.md`
* Related interfaces may live in `relay/interfaces` or in `relay/core`

## Future Enhancements

* `cabinet import/export` CLI
* `cabinet verify --signatures`
* Runtime introspection HTTP endpoint
* Version conflict resolution strategies
* AI-driven graph generation via schema analysis
