# 🚀 CIC Relay – Specification (v1)

## Overview

The CIC Relay is a decentralized, plugin-based component that:

- Loads service-specific plugins (e.g., Proxmox, Ceph, OVS).
- Makes its state, schemas, and configurations indirectly available via the **Schema Registry**.
- Has no direct GraphQL access; all data queries and responses go through the **Schema Registry**.
- Runs exclusively **without root privileges**.
- Publishes **delta-based** data to a **Message Broker** (e.g., MQTT).
- Subscribes to central instructions.
- Advertises its schemas and capabilities using uniquely identified schemas through the Schema Registry.
- Operates entirely based on configurations—no fixed roles (no agent/controller distinction).

---

## ⚡ Security Considerations: Command Execution

Some services (e.g., starting/stopping VMs, creating snapshots) may require **elevated privileges (root)** to execute certain commands.

As such:

- The Relay endpoint listens on a **dedicated IP/port** combination, protected by a **firewall** and/or **VPN**.
- All Relays run exclusively **without root privileges**.
- Commands requiring root privileges must be executed through a **sudo-wrapper** or a **dedicated systemd service**, using predefined commands.
- The Relay only executes instructions received via the message broker if:
    - The relevant topic is **whitelisted** in the configuration.
    - The instruction passes **schema validation**.
    - The associated plugin explicitly contains the logic to execute it.

Additional options:

- **`sudo` wrapper or systemd service separation** (for root-only commands).
- **Signed instructions or topic ACLs** on the MQTT side.

This ensures the Relay only executes **verified, signed, and explicitly authorized commands** with root privileges.

---

## 📦 Project Structure

```
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

