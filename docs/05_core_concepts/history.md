# History storage

<p align="center">
  <img src="/brand/logo-analytics.svg" alt="Quack Quack Telemetry and Analytics" width="80" height="80" />
</p>

Every element message (device telemetry and dashboard commands) is stored, so charts can show the past and dashboards open with values already in place. The store is a **pluggable time-series backend**, separate from the Postgres database that holds users, devices, elements, permissions and dashboards.

| Driver | What it is | When to pick it |
|---|---|---|
| `timescale` (default) | TimescaleDB, in the main Postgres or a database of its own | Simplest to run: one database for everything. Good up to millions of events a day. |
| `clickhouse` | ClickHouse (columnar) | High write volumes, long retention, analytics. Compresses from the first second. |

Changing backends is a configuration change (`QUACK_HISTORY_DRIVER`, `QUACK_HISTORY_URL`). Adding another backend (InfluxDB 3, VictoriaMetrics, QuestDB, …) is one Go package that passes the [driver contract](#adding-a-driver).

## Where dashboard data comes from

The time-series database is **not** on the live path. A value reaches a dashboard from the gateway's memory within milliseconds, and is written to the store in the background.

```mermaid
flowchart LR
  D[Device] -->|WebSocket| G[Gateway]
  G -->|"live (ms)"| B[Dashboards]
  G --> RP[(Redpanda<br/>element-events.v1)]
  RP --> GW2[Other gateways] --> B
  RP --> IN[quack ingest]
  IN -->|batches, idempotent| H[(History store<br/>timescale · clickhouse)]
  IN -->|newest value per element| ST[(element-state.v1<br/>compacted)]
  ST -->|on start| G
  H -->|replay on first subscribe<br/>charts over a range| G & API[REST API]
```

| What the dashboard needs | Served from | Cost on the store |
|---|---|---|
| Live values | the gateway's memory and the bus | none |
| The latest value when a widget opens | the gateway's memory: every gateway remembers the newest device message of every element. After a restart it reloads them from the compacted topic `element-state.v1` | none |
| The last `points` values (the replay) | the gateway's ring buffer. The first subscriber on a gateway triggers one store read; reads arriving within 5 ms are batched into a single `Last` query for all elements | one query per batch, not per widget or viewer |
| A chart range (15 min … 7 days) | `GET /api/v1/elements/{id}/history`, raw events or rollup buckets | one query per chart load, then live frames are appended |

An element with `points = 0` keeps no window but still gets its latest value on subscribe, so value widgets don't stay empty until the device speaks again.

## What is stored

Each event is stored twice, in two shapes:

| Shape | Rows | Used for |
|---|---|---|
| **Event** | One per message: time, event id, element, device, source (`device`/`user`), actor, client timestamp, the message unchanged, and its main numeric value | The replay, raw history, exports |
| **Point** | One per **numeric attribute** of the message | Aggregates of any attribute |
| **1-minute rollup** | Sum, min, max and count per element, attribute and minute | Charts over hours and days |

Attributes are flattened to paths, the same paths that [widget bindings](./dashboards.md) use:

| Message | Points |
|---|---|
| `{"value": 21.5}` or bare `21.5` | `value` = 21.5 |
| `{"temperature": 21, "humidity": "48.5", "relay": "ON", "ok": true}` | `temperature` = 21, `humidity` = 48.5 (numeric strings count), `ok` = 1 (booleans are 1/0). `"ON"` is not numeric. |
| `{"gps": {"lat": 30.04}, "sensors": [{"t": 1}]}` | `gps.lat` = 30.04, `sensors[0].t` = 1 |
| `{"x": "2026-10-07T10:00:00Z", "y": 3}` | `y` = 3, and `value` = 3 (a chart's `y` doubles as the element value) |

Limits per message: 64 points, nesting depth 8, 32 array items. Values that aren't finite (NaN, ±Inf, `1e400`) and keys a path can't address (with dots or spaces) are skipped. A message without a numeric `value` of its own gets `value` from `message.value`, a chart's `y`, or a bare number or boolean, so aggregates without a field keep working for every shape.

## Guarantees

- **At-least-once, idempotent.** The ingester commits Redpanda offsets only after the store accepted the batch. `Append` skips events it already has (by event id), so a redelivered batch changes nothing, and **rollups never count an event twice**. The contract suite checks this for every driver.
- **One bad message can't stop ingestion.** Messages that a backend can't store (a NUL character, an unpaired UTF-16 surrogate) are rejected by the gateway: device frames are dropped and counted in `quack_dropped_total{reason="unstorable"}`, browser commands get `invalid_format`. If a backend still rejects some data, the ingester splits the batch until it isolates the offending events and sends them, unchanged, to the topic **`element-events.dlq.v1`** with the reason in an `error` header (`quack_ingest_dead_letters_total`). An outage (connection refused, timeout) is retried, never dead-lettered.
- **Late data counts.** Events that arrive late, from catch-up after downtime or a backfill, are included in the rollups. TimescaleDB refreshes the affected range; ClickHouse rolls up at insert.

## Configuration

| Variable | Default | Description |
|---|---|---|
| `QUACK_HISTORY_DRIVER` | `timescale` | `timescale` or `clickhouse`. Comma-separated to write to several stores (reads use the first): see [Switching backends](#switching-backends). |
| `QUACK_HISTORY_URL` | `QUACK_DATABASE_URL` | Timescale: a Postgres URL. It can be the main database or its own. ClickHouse: `clickhouse://user:password@host:9000/database` (the database is created by `quack migrate`). |
| `QUACK_HISTORY_RETENTION` | `8760h` (365 d) | Data older than this is dropped. Applied by `quack migrate` (Timescale retention policies, ClickHouse TTL). |
| `QUACK_HISTORY_COMPRESS_AFTER` | `168h` (7 d) | Timescale only: compress chunks older than this. ClickHouse always stores compressed. |
| `QUACK_HISTORY_QUERY_TIMEOUT` | `10s` | Per history API query. A query that runs longer returns **503** instead of tying up the database. |
| `QUACK_HISTORY_MAX_QUERIES` | `16` | Concurrent history API queries per instance; more wait (then 503). |
| `QUACK_HISTORY_MAX_BUCKETS` | `1500` | Most buckets one request may ask for (`(to − from) / step`). Larger requests get **422**. The web app needs at most 360. |
| `QUACK_HISTORY_REPLAY_WINDOW` | `720h` (30 d) | How far back the replay looks for an element's last values. |

Locally, `task start HISTORY=clickhouse` (or `task infra:up` / `task up` / `task test:integration` / `task contract:go` with `HISTORY=clickhouse`) starts ClickHouse from `docker/compose.yaml` and points the platform at it.

## Choosing a driver

Measured with the benchmark in `internal/history/historytest` on the same machine. The data was 20 elements reporting once a second for 2 hours: 144,000 events, 576,000 points, one writer, batches of 1,000.

| | TimescaleDB | ClickHouse |
|---|---|---|
| Write throughput (one ingester) | ~2,500 events/s | ~5,600 events/s |
| 1-minute buckets of an attribute over 2 h | p50 15 ms | p50 22 ms |
| Newest 5,000 raw events | p50 48 ms | p50 28 ms |
| Replay: last 50 of 20 elements (30-day window) | p50 12 ms | p50 38 ms |
| Disk, first 7 days | 189 MB (kept uncompressed by default) | **8.8 MB** |
| Disk after Timescale's compression | 10 MB | 8.8 MB |

At this scale both answer every dashboard query in tens of milliseconds, so either works. The differences that decide it:
- **Write volume:** ClickHouse writes about twice as fast.
- **Disk:** ClickHouse compresses everything immediately, while Timescale keeps the last week uncompressed.
- **Operations:** ClickHouse is one more system to run, back up and monitor.

The synthetic data compresses better than real telemetry, so treat the sizes as ratios, not as absolute numbers. Re-run the benchmark with your own shape: `QUACK_IT_BENCH=1 task test:history`.

## Switching backends

No downtime and no gap:

1. **Write to both.** Set `QUACK_HISTORY_DRIVER=timescale,clickhouse` and `QUACK_HISTORY_URL=,clickhouse://…/quack` (an empty first URL means the main database). Run `quack migrate`, then restart `quack serve` and `quack ingest`. New events now go to both stores, and reads still come from the first.
2. **Copy the past.** Run `quack history copy --to-driver clickhouse --to-url clickhouse://…/quack [--since 2026-01-01T00:00:00Z]`. It walks every element in time order and is idempotent: interrupt it and run it again at will.
3. **Switch reads.** Set `QUACK_HISTORY_DRIVER=clickhouse` and `QUACK_HISTORY_URL=clickhouse://…/quack`, then restart. Once you are confident, drop the old tables.

## Adding a driver

A driver is a Go package that implements `history.Store` and registers itself:

```go
type Store interface {
    Append(ctx, []Event) (int, error)                                   // idempotent by event id
    Last(ctx, elementIDs []uuid.UUID, n int, since time.Time) (map[uuid.UUID][]Event, error)
    Events(ctx, EventQuery) ([]Event, error)                            // raw, ascending; newest-N option
    Buckets(ctx, BucketQuery) ([]Bucket, error)                         // per attribute, epoch-aligned
    Migrate(ctx) error                                                  // schema + retention, idempotent
    Reset(ctx) error                                                    // tests and dev seeding
    Close() error
}

func init() { history.Register("influxdb3", Open) }
```

`history.Points(ev)` gives the numeric attributes to store, so every backend aggregates exactly the same values. The driver is done when it passes the contract, the same suite TimescaleDB and ClickHouse pass:

```go
func TestContract(t *testing.T) { historytest.Run(t, func(*testing.T) history.Store { return st }) }
```

The contract covers:
- round-trip fidelity
- idempotency, including the rollups
- ordering and millisecond ties
- half-open ranges
- per-element `Last` with a lower bound
- aggregates of `value` and of nested, array, boolean and numeric-string attributes
- epoch-aligned buckets (5 min to 1 day)
- sub-minute buckets
- a 5,000-event batch
- `Reset`

Then import the package in `cmd/quack/main.go` (`_ "…/history/influxdb3"`) and add the backend to CI's history matrix.

## Inside each driver

| | TimescaleDB | ClickHouse |
|---|---|---|
| Events | hypertable `element_event`, unique `(time, event_id)`, `ON CONFLICT DO NOTHING` | `ReplacingMergeTree`, `ORDER BY (element_id, time, event_id)`, payload stored as text (byte-exact) |
| Points | hypertable `element_point`, unique `(time, event_id, field)` | `ReplacingMergeTree`, `ORDER BY (element_id, field, time, event_id)` |
| Rollup | continuous aggregate `element_point_1m` (real-time; late ranges refreshed explicitly) | `AggregatingMergeTree` table `element_point_1m`, fed by a materialized view at insert |
| Idempotency | unique keys, and points are written only for newly inserted events (one transaction) | checks which events exist, then writes points first and events second, so a crash in between is repaired by the redelivery |
| Retention | retention policies on all three | `TTL` on all three |
| Schema versions | goose, own table `goose_history_version` | idempotent DDL in `Migrate` |
| Replay (`Last`) | `LATERAL` newest-N per element | reverse sorting-key reads with `LIMIT n BY element_id`, in widening windows (1 h, 1 d, then the replay window) |

Schemas: [Database → History store](../06_database/schema.md#history-store).
