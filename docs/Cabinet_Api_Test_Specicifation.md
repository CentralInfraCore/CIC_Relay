# Cabinet API Test Specification

This document outlines the test requirements and structure for validating the behavior of the `Cabinet` interface implementation.

## Purpose

The goal of these tests is to ensure the Cabinet API behaves deterministically, safely, and as expected under typical and edge case conditions. Tests should cover successful operations, invalid inputs, and concurrency safety.

## Scope

Tests should validate the following API behaviors:

### 1. Schema Operations

* [ ] `RegisterSchema()` accepts a valid schema and stores it.
* [ ] `RegisterSchema()` rejects duplicate name\@version.
* [ ] `GetSchema()` returns the correct schema for a valid reference.
* [ ] `GetSchema()` returns an error for unknown reference.

### 2. Plugin Operations

* [ ] `RegisterPlugin()` registers a plugin descriptor correctly.
* [ ] `RegisterPlugin()` rejects plugins with duplicate IDs.
* [ ] `GetValidator()` returns correct plugin for schemaRef.
* [ ] `GetPlugin()` retrieves plugin by ID.

### 3. Execution Graphs

* [ ] `RegisterGraph()` accepts valid graph structure.
* [ ] `GetGraph()` returns a known graph.
* [ ] `GetGraph()` returns error for unknown ID.

### 4. Intent Resolution

* [ ] `ResolveIntent()` returns valid execution plan for known root and payload.
* [ ] `ResolveIntent()` fails gracefully for missing graph or mismatched schema.

### 5. Utility Methods

* [ ] `ListSchemas()` returns all registered schemas.
* [ ] `ListPlugins()` returns all plugin IDs.
* [ ] `ListGraphs()` returns all graph IDs.

## Concurrency Tests

* [ ] Registering schemas/plugins concurrently does not corrupt internal state.
* [ ] Parallel read operations (Get\*/List\*) are thread-safe.
* [ ] Intent resolution under concurrent load remains stable.

## Error Coverage

* Test known error cases (e.g. `ErrSchemaNotFound`, `ErrDuplicatePluginID`, `ErrInvalidGraph`)
* Confirm errors contain meaningful context messages

## Implementation Notes

* Use in-memory Cabinet implementation for unit testing
* Consider using `t.Parallel()` for concurrency validation
* Test with realistic schemas and dummy plugin descriptors
* Prefer table-driven test style for clarity and coverage
