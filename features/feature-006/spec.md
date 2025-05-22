# CIC Relay - feature-006: Introduction of the Cabinet Object

## Objective

The purpose of `feature-006` is to fully define and implement the key object of the CIC Relay system called the "Cabinet." The Cabinet serves as the central knowledge base of the system, where the Relay maintains all static structures (schema definitions, plugin descriptors, execution graphs) and uses them for routing, validation, and logic resolution.

## Context

This feature is developed under the `#CIC-DEV-FLOW-MODEL-20250522` context and encompasses the canvas-based Cabinet codebase, its internal objects, and relationships.

## Functional Components

### Static Elements

* **Schemas**: Versioned schema descriptors (from `.yaml` or `.so` sources)
* **PluginDescriptors**: Metadata of all `.so` plugins providing `Register()` symbols
* **ExecutionGraphs**: Directed graphs consisting of execution steps
* **Connections**: Logical relationships between schemas, plugins, and graph nodes

### Functional APIs

* `RegisterSchema()`
* `RegisterPlugin()`
* `RegisterGraph()`
* `ResolveIntent(graphRoot, payload)`
* `GetValidator(schemaRef)`
* `GetGraph(rootID)`

## Operational Principles

* The Cabinet serves a **stateless runtime**, and does not contain live execution state
* `.so` plugins are registered based only on `Register` function metadata
* For each `graph-intent`, the Cabinet provides the pathway but performs no execution
* Validators are also managed as versioned `.so` plugins

## Future Expansion Directions

* CLI Introspection: `cabinet ls`, `cabinet graph tree`, `cabinet plugin info`
* Signature verification: Hash + sign checks for all schemas and plugins
* Graph mutation: Dynamic graph construction logic (via AI or interface)

## Specification Requirements

* The Cabinet's full specification will reside in `cabinet/spec.md`
* All subcomponents (SchemaDef, PluginDescriptor, ExecutionGraph) will be documented
* Internal interfaces will be versioned and operate with stable, declared structures

## Build/Development Setup

* `cabinet` will be implemented as a standalone package
* Feature branch name: `feature/cabinet-core`
