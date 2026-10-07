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
