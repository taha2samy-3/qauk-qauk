# 5. Core concepts

[Architecture](../02_architecture.md) describes the components and how data moves between them. These pages explain the rules behind them: who may connect, who may see what, how changes propagate, and how dashboards are stored.

| Page | What it covers |
|---|---|
| [Authentication](./authentication.md) | Device JWTs signed with per-device keys (RS256/ES256), key rotation and revocation, user passwords (argon2id, Django hash upgrade), cookie sessions, revocation, CSRF and Origin checks |
| [Permissions](./permissions.md) | `R` / `RC` grants for users and groups, the max-permission rule, where access is enforced, and live re-evaluation on open sockets |
| [Realtime events](./realtime_events.md) | The three Redpanda topics, the CloudEvents envelope, the transactional outbox, control-event kinds, and presence events |
| [Dashboards](./dashboards.md) | The layout JSON, breakpoints, sharing rules, the widget types and how to add one |
