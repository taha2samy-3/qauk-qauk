# 5. Core concepts

[Architecture](../02_architecture.md) describes the components and how data moves between them. These pages explain the rules behind them: who may connect, who may see what, how changes propagate, and how dashboards are stored.

| Page | What it covers |
|---|---|
| [Authentication](./authentication.md) | Device JWTs signed with per-device keys (RS256/ES256), key rotation and revocation, user passwords (argon2id, Django hash upgrade), cookie sessions, revocation, CSRF and Origin checks |
| [Permissions](./permissions.md) | `R` / `RC` grants for users and groups, the max-permission rule, where access is enforced, and live re-evaluation on open sockets |
| [Realtime events](./realtime_events.md) | The Redpanda topics, the CloudEvents envelope, the transactional outbox, control-event kinds, presence events, and `device-config.v1` (the device registry) |
| [Rate limits](./rate_limits.md) | Per-element limits (drop or keep latest), the per-device guard, several gateway instances, and why there is no shared limiter (yet) |
| [Dashboards](./dashboards.md) | The layout JSON, breakpoints, sharing rules, the widget types and how to add one |
