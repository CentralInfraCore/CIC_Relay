# CIC Relay

The CIC Relay is an ultra-minimalist, declarative, plugin-based execution engine designed for decentralized communication, configuration, and state management.
It tightly integrates with the Schema Registry to provide a unified, schema-driven approach to handling service-specific logic and workflows.

---

## Core Principles and Features

* **Plugin Architecture**
  All functionality, processing logic, validation, and compliance is implemented exclusively as modules (plugins).
  Each Relay instance can load service-specific plugins (e.g., Proxmox, Ceph, Open vSwitch), each with their own schema, resolvers, and logic.

* **Declarative Template–Instance Model**
  Every workflow, module, and schema exists only as a template in the registry.
  At runtime, only workflow instances are created, referencing the templates.
  Pipelines, modules, and schemas are never duplicated—this ensures minimal memory usage and maximum scalability.

* **Schema-Driven Communication and Validation**
  All data and state management is based on explicit schemas.
  The Relay interacts with the Schema Registry to publish and retrieve schemas and data, ensuring consistent, versioned, and auditable communication.

* **Delta-Based Updates**
  State changes (“deltas”) are pushed to a message broker, so only relevant updates are transmitted—minimizing network load.

* **Audit and Compliance**
  Each workflow instance maintains a full process and state log.
  The system is deterministic and fully traceable—making it easily auditable and compliance-ready for enterprise environments.

* **Object Registry and Fast Lookup**
  Every template and instance has a unique ID and is stored in an indexed, O(1)-lookup registry.
  This provides fast searching, even under high parallel workloads.

* **Built-in Security**
  All operations run without root privileges by default.
  Commands requiring elevated privileges are handled with strict controls, via dedicated wrappers or service-level isolation.

* **Modular and Extensible**
  New services, logic modules, workflows, or schemas can be added simply by registering new templates and plugins.
  All new features are integrated instantly, with no need to rebuild the core.

---

## Pluggable Communication and Integration Modules

CIC Relay’s architecture fully supports integration with any communication or orchestration technology—**GraphQL**, **message queues** (NATS, MQTT, RabbitMQ, Kafka), or even REST APIs—as plugins or external services within the pipeline.

* **GraphQL**:

  * Can be integrated as a pipeline module/step or as a separate API service/adapter.
  * Enables schema-driven data handling, introspection, and automatic documentation.
* **Message Queue (messagesQ):**

  * Supports async event-dispatch or orchestration, either as plugins or external orchestrators.
  * Full audit and compliance support—every transaction through the message queue can be logged and verified.

**All such integrations are handled via explicit, declarative, pipeline-based plugin interfaces.
There are no “baked-in” technology dependencies. This guarantees long-term flexibility and extensibility.**

---

## Project Structure

```plaintext
cic-relay/
├── cmd/                    # Main entrypoint binaries
├── core/
│   ├── registry/           # Object registry implementation
│   ├── pipeline/           # Pipeline execution
│   ├── plugins/            # Pluggable modules (Go interfaces)
├── api/
│   ├── graphql/            # GraphQL (or REST) adapters
│   ├── mq/                 # Message queue interfaces
├── pkg/
│   ├── schema/             # Schema definitions and validators
│   └── util/               # Utility functions
├── config/                 # Configuration examples, templates
├── docs/                   # Documentation, diagrams, pipeline examples
├── tests/                  # Tests, pipeline test cases
```

> *This structure fits the declarative, plugin-based, template–instance architecture.
> Folders can be adapted or extended as the system evolves.*

---

## Glossary (Core System Concepts)

| Type     | Template (definition)           | Instance (object)                      |
| -------- | ------------------------------- | -------------------------------------- |
| Workflow | workflow\_template              | workflow\_instance / workflow\_obj     |
| Module   | module\_template                | module\_obj (within workflow instance) |
| Schema   | schema\_template                | (validation result only, no instance)  |
| Registry | Central store, all template/IDs | Instance registry for state only       |

---

## How It Works

1. **On startup:** All workflow, module, and schema templates are loaded into the registry (e.g., from YAML/JSON or a database).
2. **Workflow trigger:** When a new event or input arrives, a unique workflow instance is created with its own state and ID.
3. **Pipeline execution:** The instance traverses the declarative pipeline, referencing the corresponding module and schema templates at each step.
4. **State and log management:** All processing results, logs, and audit trails are tied exclusively to the instance.
5. **Extensibility:** Adding new modules, workflows, or schemas only requires registering new templates.

---

## Example Pipeline Descriptor (YAML)

```yaml
pipeline:
  steps:
    - id: validate_user
      module: validate_user
      input_schema: user_schema
      output_schema: validated_user_schema
    - id: compliance
      module: gdpr_compliance
      input_schema: validated_user_schema
      output_schema: compliant_user_schema
```

---

## Architecture Sketch (ascii)

```
[workflow_template]--+
                     |
                +----v----+
                | workflow_instance |
                +----+----+
                     |
  +------------------+-------------------+
  |                  |                   |
[module_template] [module_template] ... [schema_template]
  |                  |                   |
(instance state/log changes only in the instance)
```

---

## Note

This description reflects the current Go-based, template–instance architecture, plugin-based pipeline model, and registry-centric operation.
Compared to the earlier Python-based approach, the emphasis is now on scalability, auditability, plugin modularity, and a declarative workflow mindset.

---

## License

Creative Commons Attribution-NonCommercial-ShareAlike 4.0 International (CC BY-NC-SA 4.0)
