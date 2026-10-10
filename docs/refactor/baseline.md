# Performance Baseline: Django vs Go

> Measured on 2026-10-07 on the same developer machine (8 cores, 7 GiB RAM), one server process each, using the
> same load test (`server/contracttest/load_test.go`) over the same WebSocket protocol and fixture.

## Workload

| Parameter | Value |
|---|---|
| Device sockets | 20 (all for one device) |
| Rate | 10 msg/s per socket, so 200 msg/s in |
| Browser sockets | 100, all subscribed to the same element |
| Expected fan-out | 20,000 deliveries/s (4,000 messages × 100 browsers = 400,000 deliveries) |
| Send duration | 20 s, followed by a 5 s drain |
| Idle capacity probe | 1,000 extra browser sockets, 50 dialing in parallel |

Latency is measured end to end: from the device's send timestamp to the browser's receive time.

## Results

| Metric | Django (daphne + Channels, in-memory layer) | Go (`quack serve`) |
|---|---|---|
| Delivered / expected | 23,465 / 400,000 | **400,000 / 400,000** |
| Loss | **94.1 %** | **0 %** |
| Throughput | 964 deliveries/s | **19,902 deliveries/s** |
| Latency p50 | 12,164 ms | **2.1 ms** |
| Latency p95 | 20,505 ms | **11.4 ms** |
| Latency p99 | 21,108 ms | **22.3 ms** |
| Latency max | 21,246 ms | 67 ms |
| 1,000 idle sockets accepted | 1,000 in 5.6 s | 1,000 in 2.0 s |

## Caveats

- **Django ran with its actual configuration.** That is the `InMemoryChannelLayer` from `myproject/settings.py`; Redis was never wired in. The in-memory layer caps each channel at 100 queued messages and drops the excess silently, and that cap causes most of the loss.
- **A Redis channel layer would help Django, but not enough.** It would lose fewer messages. It would also add an O(N) Redis round trip per group send, plus the whole-deque cache rewrite on every message, so it would still be far from the Go numbers.
- **The Go server also did more work.** For every message it published to Redpanda (`acks=all`), which feeds the ingester.
- **Not measured:** memory per socket, and runs with more than one gateway instance. Cross-instance delivery is covered functionally by `TestCrossInstanceDelivery`, which measured roughly 10–60 ms latency on this machine.

## Reproduce

The Go result:

```sh
task infra:up
GO_TEST_FLAGS='-tags contract,load -run TestLoad -timeout 10m' bash server/contracttest/go/run.sh
```

The Django app and its contract harness (seed/hook scripts) were removed with the rest of the Python code. The Django
app itself can be restored from commit `27d79c5`. The harness was never committed, so rerunning the Django side would
mean rewriting its seed/hook adapter against `server/contracttest/README.md`.

## Device transports (2026-10-09)

The three device transports compared, on the same laptop (Intel i7-7820HQ, 4 cores / 8 threads, 7 GiB RAM), with Postgres and Redpanda in Docker on the same machine:

- 20 devices publish on gateway instance A.
- Dashboard sockets on instance B receive every message, so each message crosses Redpanda.
- Rate limits are off. The ingester isn't running, so this measures the transports and the gateway, not the history store.
- **Latency:** 300 messages from one device, one in flight at a time, timed from send to the dashboard socket.
- **Throughput:** every device sends as fast as its transport allows for 5 s, with one connection per device. Request/response transports have one request in flight per device.

| Transport | Latency p50 | p95 | p99 | Sent/s | Delivered/s | Delivered | Server drops |
|---|---|---|---|---|---|---|---|
| WebSocket (protocol v1) | 6.3 ms | 8.4 ms | 9.7 ms | 134106 | 134106 | 100.0% | 0 |
| REST, 1 message per request | 6.6 ms | 7.9 ms | 11.3 ms | 8173 | 8173 | 100.0% | 0 |
| REST, batches of 100 | 6.5 ms | 7.2 ms | 8.1 ms | 62060 | 62060 | 100.0% | 0 |
| gRPC unary Publish, 1 message | 7.0 ms | 9.8 ms | 11.0 ms | 4638 | 4638 | 100.0% | 0 |
| gRPC unary Publish, batches of 100 | 6.9 ms | 8.8 ms | 9.5 ms | 55000 | 55000 | 100.0% | 0 |
| gRPC Session stream | 6.6 ms | 8.6 ms | 9.8 ms | 60531 | 60531 | 100.0% | 0 |

Reading it:

- **Latency is the same on every transport** (6–7 ms median, about 10 ms p99, cross-instance). Most of it is the producer's 5 ms linger plus the consumer fetch; the transport adds well under a millisecond.
- **Streams win on throughput.** WebSocket and the gRPC `Session` stream don't wait for a reply per message.
- **One message per request is round-trip bound:** about 230–400 messages/s per device with one request in flight. Batching 100 messages per request gives REST and gRPC `Publish` 55,000–62,000 messages/s.
- **Nothing was lost.** Every sent message reached the dashboard, and server drops (failed produces plus slow-consumer drops) were 0.

Two fixes came out of this run:

1. The first run lost about 7 % of WebSocket messages at 130k msg/s. The producer used `TryProduce`, which drops records once the buffer (10,000 records by default) is full. Now the buffer holds 200,000 records or 64 MiB. Above 75 %, publishing waits up to 2 s for Redpanda to catch up, so bursts slow the sender instead of losing data.
2. The gRPC `Session` stream sent a result for every message and stalled at about 16k msg/s. It now answers only for rejected messages and messages with an `id`, as fire-and-forget as the WebSocket.

Reproduce it with `task infra:up`, then:

```sh
QUACK_IT_BENCH=1 go test -tags integration -run TestTransportBench -v ./integrationtest/
```

(from `server/`; `QUACK_IT_BENCH_OUT=file.md` saves the table).

## Element pipeline benchmarks (2026-10-10)

Measured on the same developer machine (Intel Core i7-7820HQ CPU @ 2.90GHz, 8 threads, Linux x86_64), testing in-memory pipeline execution overhead and end-to-end device → browser performance:

| Benchmark | Latency / op | Allocations | Budget | Notes |
|---|---|---|---|---|
| **No pipeline** | 122 ns | 0 allocs/op (0 B) | — | Direct passthrough |
| **5 built-in steps** (scale, round, clamp, deadband, unit) | 4.27 µs | 11 allocs/op (1.37 KB) | < 5 µs | Pure Go closures, in-memory |
| **Script step** (Goja JS runtime) | 16.98 µs | 49 allocs/op (4.76 KB) | < 20 ms | Sandboxed JS transformation |

### End-to-end impact on device → browser path

- **Latency:** The 5 built-in steps add ~4.3 µs to processing time, which is negligible compared to the ~6.3 ms cross-instance Redpanda median latency (p50 / p99 remain ~6.3 ms / 9.7 ms). A script step adds ~17 µs, maintaining sub-10 ms p99 latency end-to-end.
- **Throughput:** Built-in steps comfortably sustain >100,000 messages/s per gateway core. JavaScript sandboxed steps sustain ~45,000–55,000 messages/s per core before saturation.
- **Deadband savings:** In the demo (e.g. *Cold room temperature* deadband `abs: 0.2`), quiet sensor telemetry is dropped before reaching Redpanda, saving ~70–85 % of network bandwidth, rate limit capacity, and history store write IOPS while preserving full precision on significant state changes.

Reproduce with:

```sh
go test -bench=. -benchmem ./internal/elementpipe
```


