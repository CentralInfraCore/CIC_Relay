# Feature List

# Feature List

## Overview
This document contains a list of all the features that are either planned, under development, or already completed. Each feature has an ID, title, and status to track progress.

---

## Features
- **Feature-001**: Administering new features and specifications.
    - **Status**: Active
    - **Branch**: `d/feature-001`
    - **Description**: This feature is dedicated to managing new feature development, administrating specifications, and organizing the overall project direction.
    - **Spec Document**: [admin-dashboard-spec.md](features/feature-001/spec.md)

- **Feature-002**: Developing and managing the git management system.
    - **Status**: Completed 
    - **Branch**: `d/feature-002`
    - **Description**: This feature is focused on creating and managing the version control rules and the git workflow.
    - **Spec Document**: [spec.md](features/feature-002/spec.md)
    - **Completion Date**: 2025-03-05

- **Feature-003**: Specification: CIC Relay
    - **Status**: Completed 
    - **Branch**: `d/feature-003`
    - **Description**: This document outlines the specifications and expectations for the CIC Relay, to be merged under the feature-003 branch.
    - **Spec Document**: [spec.md](features/feature-003/spec.md)
    - **Completion Date**: 2025-03-21

- **Feature-005**: Build Environment Initialization (Go-based CIC Relay)
    - **Status**: In Progress
    - **Branch**: `d/feature-005`
    - **Description**: Defines and initializes the modular build and development environment for the CIC Relay component using Go. Supports containerized development, Makefile-driven workflows, and optional event propagation via MQ.
    - **Spec Document**: [spec.md](features/feature-005/spec.md)
    - **Completion Date**: *(TBD)*
- **Feature-006**: Introduction of the Cabinet Object
    -  **Status**: Active
    -  **Branch**: `d/feature-006`
    -  **Description**: Implements the Cabinet object as the central static knowledge registry for schemas, plugins, and execution graphs.
    - **Spec Document**: [spec.md](features/feature-006/spec.md)
    - **Completion Date**: *(TBD)*

- **Feature-008**: Capability-Based Secret Flow (Vault Connection)
    -  **Status**: Concept
    -  **Branch**: `d/feature-008`
    -  **Description**: Trust model for obtaining/using a Vault secret/token without the plaintext ever existing as data outside a controlled boundary. A WASM module generates a Cert whose CNs encode task content; the Relay countersigns (content-dependent authorization); Vault cert-auth issues a scoped token; every returned secret is stored encrypted to the request's public key and used only through the WASM module's endpoint (pipeline). The first concrete `Connection` type on top of feature-006.
    - **Spec Document**: [spec.md](features/feature-008/spec.md)
    - **Completion Date**: *(TBD)*
