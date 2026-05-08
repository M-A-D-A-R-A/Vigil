# Vigil

Vigil is a local-first observability box for side projects.

This repository is a single monorepo for:

- the Go backend
- the React/Vite UI
- tests
- user-facing docs

## Quick start

1. Install Go and Bun.
2. Run `make ui-install`.
3. Run `make test`.
4. Run `make ui-build`.
5. Run `make run`.

The app starts on `http://localhost:8080`.

## Useful commands

- `make run` - run the Go server locally
- `make ui-dev` - start the Vite dev server
- `make test` - run backend tests for the Go packages in this repo
- `make ui-build` - build the frontend into `web/dist`
- `make build` - build the Go binary into `bin/vigil`
- `make bench` - create a benchmark project, ingest synthetic logs, and time log queries
- `make size` - print Go binary size, UI bundle size, and combined shipped size
- `make size-release` - print stripped release binary size, UI bundle size, and combined release size
- `make build-linux-arm64` - build a Raspberry Pi 4 friendly Linux ARM64 release binary

## First run

1. Start Vigil with `make run`
2. Open `http://localhost:8080`
3. Create a project in the UI
4. Copy the one-time ingest key
5. Send the generated `curl` example
6. Watch the event appear in Logs, Traces, and Stats

## Retention

Vigil keeps data forever unless retention is explicitly enabled.

Set these environment variables to turn it on:

- `VIGIL_RETENTION_ENABLED=true`
- `VIGIL_RETENTION_DAYS=30`
- `VIGIL_RETENTION_SWEEP_INTERVAL=1h`
- `VIGIL_RETENTION_DRY_RUN=false`

Retention works on raw UTC day folders. When a sweep removes expired raw folders, Vigil clears and rebuilds the SQLite read model from the remaining NDJSON so the query surfaces stay consistent.

## Repository layout

- `cmd/vigil` - binary entrypoint
- `internal/` - backend application code
- `ui/` - React + TypeScript + Vite frontend
- `web/dist` - built frontend assets
- `docs/` - usage and operations docs
- `test/` - integration, e2e, and fixtures

## Log pipeline benchmark

Latest baseline: Vigil accepted 100,000 log events through the public API with
32 parallel ingest workers and no event loss. After batched SQLite indexing,
ingest throughput was about 4,939 events/sec, ingest p95 latency was 23.4ms,
recent log listing was 14.2ms p95, and full-text search was 237.2ms p95. Async
indexing caught up 270.5ms after ingest finished, so all 100k logs were
queryable after about 20.52s total. See [benchmark.md](benchmark.md) for the
full before/after output, file sizes, and readout.

Run a local isolated benchmark. By default, raw `.ndjson` files are written under
`./vigil-bench-data/logs/<project_id>/<date>/`.

```sh
make bench ARGS="-events 5000 -concurrency 16 -query-runs 25"
```

Benchmark flags:

- `-events` is the total number of generated log events to send.
- `-concurrency` is the number of parallel ingest workers. For example, `-concurrency 32` means 32 workers send `POST /api/ingest` requests at the same time until all events are sent.
- `-query-runs` is the number of timed query repetitions after ingest and indexing.
- `-warmup-runs` is the number of untimed query warmups before measuring.
- `-wait-timeout` controls how long the benchmark waits for async indexing to catch up.
- `-request-timeout` controls each HTTP request timeout.
- `-data-dir` overrides the default repo-local benchmark directory.
- `-addr` benchmarks an already-running Vigil server instead of starting an isolated local server.

Run the 100k log benchmark and save document-ready output:

```sh
mkdir -p vigil-bench-data
make bench ARGS="-events 100000 -concurrency 32 -query-runs 25 -warmup-runs 3 -wait-timeout 5m -request-timeout 60s" | tee vigil-bench-data/benchmark-100000-logs.txt
```

Compare concurrency levels:

```sh
make bench ARGS="-events 100000 -concurrency 1 -query-runs 10"
make bench ARGS="-events 100000 -concurrency 8 -query-runs 10"
make bench ARGS="-events 100000 -concurrency 32 -query-runs 10"
make bench ARGS="-events 100000 -concurrency 64 -query-runs 10"
```

Benchmark a running Vigil server instead:

```sh
make bench ARGS="-addr http://localhost:8080 -events 5000 -concurrency 16"
```
