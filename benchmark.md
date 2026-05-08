# Benchmarks

This file tracks local benchmark runs for Vigil's log pipeline.

## 100k Log API Benchmark

Run date: 2026-05-08

Command:

```sh
make bench ARGS="-events 100000 -concurrency 32 -query-runs 25 -warmup-runs 3 -wait-timeout 5m -request-timeout 60s" | tee vigil-bench-data/benchmark-100000-logs.txt
```

Workload:

- Created a new benchmark project via `POST /api/projects`.
- Ingested 100,000 generated `kind=log` events via `POST /api/ingest`.
- Used `-concurrency 32`, meaning 32 parallel workers sent ingest requests at the same time until all 100,000 events were accepted.
- Waited for async SQLite indexing to catch up.
- Queried the same data through `GET /api/logs`.
- Wrote raw `.ndjson` files under `vigil-bench-data/logs/<project_id>/<date>/`.

Benchmark flag meanings:

- `-events 100000`: send 100,000 total generated log events.
- `-concurrency 32`: run 32 parallel ingest workers. This simulates a bursty app where many threads or services write logs at once.
- `-query-runs 25`: measure each query 25 times after indexing catches up.
- `-warmup-runs 3`: run each query 3 untimed times before measurement.
- `-wait-timeout 5m`: wait up to five minutes for async indexing to reach the expected total.
- `-request-timeout 60s`: allow each API request up to 60 seconds before failing.

Result:

```text
Benchmark project: bench-20260508-130457-52101 (proj_8f35bed513da7a2930797699)
Server: http://127.0.0.1:50345
Data dir: vigil-bench-data
Raw logs dir: vigil-bench-data/logs/proj_8f35bed513da7a2930797699/2026-05-08
SQLite db: vigil-bench-data/index/vigil.db

Ingesting 100000 log events with concurrency 32...
Ingest summary:
  successful: 100000/100000
  elapsed:    16.586s
  throughput: 6029.4 events/s, 2.29 MiB/s
  latency:    avg=5.3ms p50=4.4ms p95=12ms p99=17.4ms max=74.5ms
Waiting for async index catch-up...
Index catch-up: 27.874s, indexed total=100000
Storage summary:
  request payload: 39778890 bytes (37.94 MiB)
  raw ndjson:      5 files, 48467821 bytes (46.22 MiB)
  sqlite db:       115851264 bytes (110.48 MiB)
  sqlite index:    121532584 bytes (115.90 MiB)
  data dir total:  170000969 bytes (162.13 MiB)
Query summary:
  all recent logs    total=100000 avg=12.1ms   p50=12.1ms   p95=13.4ms   p99=13.7ms   max=13.8ms   ok
  level=error        total=5000   avg=58.1ms   p50=55.8ms   p95=58.5ms   p99=67.7ms   max=105.9ms  ok
  q=checkout         total=25000  avg=235.5ms  p50=233.2ms  p95=251.6ms  p99=261.5ms  max=263.2ms  ok
  name=auth.failed   total=2000   avg=57.4ms   p50=57.4ms   p95=58.7ms   p99=59.4ms   max=59.5ms   ok
```

File sizes:

```text
162M  vigil-bench-data
 50M  vigil-bench-data/logs
112M  vigil-bench-data/index
```

Raw segment files:

```text
vigil-bench-data/logs/proj_8f35bed513da7a2930797699/2026-05-08/0001.ndjson  10M
vigil-bench-data/logs/proj_8f35bed513da7a2930797699/2026-05-08/0002.ndjson  10M
vigil-bench-data/logs/proj_8f35bed513da7a2930797699/2026-05-08/0003.ndjson  10M
vigil-bench-data/logs/proj_8f35bed513da7a2930797699/2026-05-08/0004.ndjson  10M
vigil-bench-data/logs/proj_8f35bed513da7a2930797699/2026-05-08/0005.ndjson  6.2M
```

## Readout

This is a good local-first baseline:

- Ingest accepted all 100,000 events with no loss.
- Ingest throughput was about 6,029 events/sec.
- Ingest p95 latency was 12ms.
- Recent log listing stayed fast at 13.4ms p95.
- Filtered queries stayed around 58ms p95.
- Full-text search was the expensive path at 251.6ms p95 for `q=checkout`.

Concurrency readout:

- `concurrency 32` does not mean 32 projects or 32 servers.
- It means one benchmark project with 32 client workers repeatedly calling `POST /api/ingest`.
- The 100k run shows Vigil can accept a 32-way ingest burst without event loss after the benchmark client was fixed to reuse HTTP connections.
- Future comparisons should run the same 100k profile at concurrency 1, 8, 32, and 64 to find the point where SQLite write contention or HTTP overhead starts to dominate.

The main watch item is async indexing lag:

- Ingest elapsed: 16.586s.
- Index catch-up after ingest: 27.874s.
- Total time until all 100k logs were queryable: about 44.46s.
- Effective indexed rate over total time: about 2,249 events/sec.

## Notes

The first 100k attempt exposed a benchmark-client problem: the client was not draining ingest responses, so short-lived connections exhausted local TCP ports and produced `can't assign requested address`. The benchmark now reuses HTTP connections properly.

## 100k Log API Benchmark After Batched SQLite Indexing

Run date: 2026-05-08

Command:

```sh
make bench ARGS="-data-dir vigil-bench-data-batched -events 100000 -concurrency 32 -query-runs 25 -warmup-runs 3 -wait-timeout 5m -request-timeout 60s" | tee vigil-bench-data-batched-100000-logs.txt
```

Result:

```text
Benchmark project: bench-20260508-150020-26293 (proj_ac1f04655c40b8cba7c115b8)
Server: http://127.0.0.1:51873
Data dir: vigil-bench-data-batched
Raw logs dir: vigil-bench-data-batched/logs/proj_ac1f04655c40b8cba7c115b8/2026-05-08
SQLite db: vigil-bench-data-batched/index/vigil.db

Ingesting 100000 log events with concurrency 32...
Ingest summary:
  successful: 100000/100000
  elapsed:    20.248s
  throughput: 4938.7 events/s, 1.87 MiB/s
  latency:    avg=6.5ms p50=4ms p95=23.4ms p99=45.9ms max=85.6ms
Waiting for async index catch-up...
Index catch-up: 270.5ms, indexed total=100000
Storage summary:
  request payload: 39778890 bytes (37.94 MiB)
  raw ndjson:      5 files, 48467748 bytes (46.22 MiB)
  sqlite db:       116011008 bytes (110.64 MiB)
  sqlite index:    122376248 bytes (116.71 MiB)
  data dir total:  170843996 bytes (162.93 MiB)
Query summary:
  all recent logs    total=100000 avg=13.3ms   p50=13.1ms   p95=14.2ms   p99=14.9ms   max=15.8ms   ok
  level=error        total=5000   avg=57.7ms   p50=56.7ms   p95=61.1ms   p99=61.8ms   max=62.8ms   ok
  q=checkout         total=25000  avg=232.2ms  p50=232.3ms  p95=237.2ms  p99=238.8ms  max=287.1ms  ok
  name=auth.failed   total=2000   avg=58.2ms   p50=58.5ms   p95=59.9ms   p99=60ms     max=60.2ms   ok
```

Before/after readout:

- Index catch-up improved from 27.874s to 270.5ms.
- Total time until all 100k logs were queryable improved from about 44.46s to about 20.52s.
- Ingest stayed lossless at 100,000/100,000 accepted events.
- Ingest p95 regressed from 12ms to 23.4ms, likely because the indexer now keeps up during ingestion instead of leaving most work for after ingestion.
- Full-text search p95 improved from 251.6ms to 237.2ms.
- Storage stayed essentially flat: 162.13 MiB before, 162.93 MiB after.
