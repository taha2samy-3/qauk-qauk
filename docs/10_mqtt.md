# MQTT Adapters

The Quack Quack platform supports connecting to external MQTT brokers as a client (subscriber and publisher).
**We are a subscriber, not a broker.** There is no embedded broker. The `mqtt` gateway connects to your existing brokers (Mosquitto, EMQX, AWS IoT, etc.) over MQTT 5.

## Role and Setup

To enable MQTT capabilities, add the `mqtt` role to `QUACK_ROLES` (e.g., `QUACK_ROLES=api,gateway,mqtt`).
A gateway with the `mqtt` role will automatically pick up configured MQTT connections and maintain connections to the brokers.

## Connection Distribution (HRW)

Connections are spread over all live gateways with the `mqtt` role using Highest Random Weight (HRW) Rendezvous Hashing.
This ensures an even distribution of connections and automatic failover without a central coordinator.
If `replicas > 1`, the broker will use MQTT 5 Shared Subscriptions to balance messages among the replicas.

## Authentication

Supported broker authentication methods:
- **mTLS (Recommended)**: Client certificates.
- **JWT**: Signed with a key secret.
- **Username/Password**: Supported over TLS.

> **Broker ACL Advice**: Ensure per-device credentials and strict topic patterns, as any publisher on a subscribed topic can feed data into the platform.

## Rules and Decoders

- **Uplink Rules**: Map incoming MQTT topics to platform devices and elements.
- **Source Pipeline**: Processes uplinks via an optional TTN/ChirpStack-compatible JavaScript decoder, followed by a declarative field map.
- **Downlink Rules**: Forward platform commands back to the broker on templated topics.

## Troubleshooting

The platform stores detailed connection statuses and standard MQTT 5 Reason Codes (e.g., `0x86` for bad credentials). Check the admin UI for connection health.
