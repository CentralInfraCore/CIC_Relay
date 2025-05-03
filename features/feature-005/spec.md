# CIC Relay - feature-005 Specification

## Purpose

The goal of `feature-005` is to establish a modern development and build environment for the CIC-relay component. This setup should support the full lifecycle, from greenfield (clean-slate) setup to smooth, modular development, testing, and containerized execution.

## Transition Motivation

The objective of this feature is not to implement application logic, but to lay the groundwork for a structured Go-based development environment. This includes replacing the previous Python-based prototype with a more robust and scalable Go implementation.

### Why transition from Python to Go?

* **Performance and concurrency**: Go's built-in concurrency (via goroutines) offers clear advantages for relay-type systems.
* **Static typing**: Useful for catching issues early when dealing with complex schemas and validation logic.
* **Single binary deployment**: Go produces statically linked binaries, simplifying CI/CD and deployment.
* **Memory efficiency**: Suited for high-throughput, cache-heavy systems.
* **Professional tooling**: Built-in support for testing, module management, and linting (`go test`, `go mod`, `golangci-lint`).

The first step is to design a structured build and development environment that aligns with the architecture of the CIC-relay. Key principles:

* Each component has a standalone folder and entry point.
* A central `Makefile` orchestrates local development, testing, building, and (optionally) container execution.
* `docker compose` launches a minimal environment (relay, MQ, dummy agent).
* A persistent build container provides a shared Go workspace for local builds.
* `make` targets (`make run`, `make build`, `make test`) are designed to be CI-compatible.
* Metrics and event messages can optionally be sent to a local or central Message Queue (e.g., NATS, Mosquitto).

The environment is intended to be decentralized and fully functional in local developer setups, not reliant on centralized infrastructure.

### Development Environment Scope

The environment is built to support the entire development pipeline—from zero to production-readiness:

* Modular and container-friendly structure
* Declarative operation via Makefile targets
* `docker compose` integration for running minimal sets of services
* Message Queue support lays the foundation for event-driven components. Currently planned uses include:

  * Reporting **failed events** with references to logs or error types
  * Broadcasting **current execution state** (build, verify, deploy phases)
  * Publishing **system component statuses** (availability, activity, last response)
* The system supports both greenfield and grayfield initialization scenarios (empty or partially populated object sets), using default bootstrapped structures if needed.
