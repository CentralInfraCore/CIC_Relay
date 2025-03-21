# CIC Relay

The CIC Relay is a lightweight, plugin-based framework designed to handle decentralized communication, configuration, and state management. It integrates seamlessly with the Schema Registry to provide a unified, schema-driven approach to managing service-specific logic.

---

## Key Features

- **Plugin-Based Architecture:**
  Each Relay instance loads one or more service-specific plugins, such as Proxmox, Ceph, or Open vSwitch, to perform tasks and handle queries related to those services.

- **Schema-Driven Communication:**
  Instead of direct API calls, the Relay relies on the Schema Registry to publish and retrieve schemas and data, ensuring consistent, versioned communication.

- **Delta-Based Updates:**
  The Relay publishes changes (deltas) to a message broker, minimizing bandwidth and ensuring that only relevant state changes are transmitted.

- **Security by Design:**
  All operations run without root privileges by default. Commands requiring elevated privileges are handled through strict controls, such as dedicated wrappers or service-level separation.

- **Modular and Extensible:**
  Additional services can be integrated through new plugins. Each plugin comes with its own schema, resolvers, and logic, making the system highly adaptable to new environments.

---

## Project Structure

```plaintext
cic-relay/
├── main.py                  # Entry point
├── config.yaml              # Global configuration
├── graphql/
│   ├── schema_router.py     # Combines GraphQL schemas from plugins
│   └── resolvers/           # Optional shared resolvers
├── mqtt/
│   ├── client.py            # MQTT publish/subscribe logic
│   ├── topic_map.yaml       # Topic → handler mapping
│   └── message_handler.py   # Topic handler dispatcher
├── plugins/
│   ├── base.py              # PluginBase class and interface
│   ├── proxmox/
│   │   ├── plugin.py        # Specific plugin implementation
│   │   ├── schema.graphql   # GraphQL SDL definition
│   │   └── schema.crc       # CRC/signature for the schema
├── utils/
│   ├── crc.py               # CRC/hash generation and verification
│   ├── diff.py              # Compares default vs current data
│   └── schema_loader.py     # Loads plugin schemas
├── tests/                   # Unit tests (pytest + coverage)
│   ├── test_plugin_loader.py
│   ├── test_mqtt_pubsub.py
│   └── ...
```

