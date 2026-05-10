# Vigil

Vigil is a local-first observability box for side projects. It gives you a built-in UI for logs, traces, and stats, stores data locally, and uses project-scoped ingest keys so each app can send events without a larger observability stack.

## Install

Install the latest GitHub Release:

```sh
curl -fsSL https://raw.githubusercontent.com/M-A-D-A-R-A/Vigil/main/scripts/install.sh | sh
```

Then start Vigil:

```sh
vigil serve
```

Vigil listens on `http://localhost:8080` by default.

## First Run

In your app or project directory, initialize Vigil:

```sh
vigil init -project my-app
```

`vigil init` creates or selects a project, stores your local CLI config, writes SDK-ready values into `.env`, and prints a ready-to-run ingest command.

Send your first event:

```sh
vigil ingest-command
```

Run the printed `curl` command, then open `http://localhost:8080` and watch the event appear in Logs, Traces, and Stats.

## Using The CLI

- `vigil` - start the server
- `vigil serve` - explicitly start the server
- `vigil init [-project NAME]` - create or select a project and write app env values
- `vigil status` - show the active server, project, key state, and health
- `vigil projects` - list projects and mark the active one
- `vigil use PROJECT_ID_OR_NAME` - switch active project
- `vigil key rotate` - create and store a fresh ingest key for the active project
- `vigil ingest-command` - print a ready-to-run curl command

The CLI defaults to `http://localhost:8080`, then reuses the saved server URL. Set `VIGIL_CONFIG_PATH` to use a specific config file.

## App Environment

`vigil init` manages only this block in your app `.env` and preserves the rest of the file:

```env
# BEGIN VIGIL
VIGIL_BASE_URL=http://localhost:8080
VIGIL_PROJECT_ID=proj_...
VIGIL_INGEST_KEY=vigil_...
# END VIGIL
```

Use `vigil init -env-file PATH` to write a different env file. Server runtime settings, such as retention and data directories, stay in the server environment.

## Data Storage

`vigil init` does not choose where log files live. It configures your app to send data to a running Vigil server. The server owns the on-disk data directory.

By default, `vigil serve` writes data under `./vigil-data` relative to the directory where the server is started:

```txt
vigil-data/
├── logs/
│   └── <project_id>/
│       └── <YYYY-MM-DD>/
│           └── *.ndjson
└── index/
    └── vigil.db
```

The raw NDJSON files are the source of truth. `vigil.db` is the SQLite read model used by the UI and query APIs, and Vigil can rebuild it from the remaining NDJSON data when needed.

Use `VIGIL_DATA_DIR` to put server data somewhere specific:

```sh
VIGIL_DATA_DIR=/path/to/vigil-data vigil serve
```

## Development

Install Go and Bun, then build and run locally:

```sh
make ui-install
make ui-build
make build
bin/vigil
```

Useful development commands:

- `make run` - run the Go server locally
- `make ui-dev` - start the Vite dev server
- `make test` - run backend tests
- `make smoke` - build the UI and run the browser first-run/explorer smoke test
- `make bench` - ingest synthetic logs and time log queries
- `make size-release` - print stripped release binary size plus embedded UI size
- `make build-linux-arm64` - build a Raspberry Pi 4 friendly Linux ARM64 binary

For local frontend development without rebuilding the embedded UI:

```sh
make run
make ui-dev
```

Open `http://localhost:5173`.

## Retention

Vigil keeps data forever unless retention is explicitly enabled.

Set these environment variables to turn it on:

- `VIGIL_RETENTION_ENABLED=true`
- `VIGIL_RETENTION_DAYS=30`
- `VIGIL_RETENTION_SWEEP_INTERVAL=1h`
- `VIGIL_RETENTION_DRY_RUN=false`

Retention works on raw UTC day folders. When a sweep removes expired raw folders, Vigil clears and rebuilds the SQLite read model from the remaining NDJSON so query surfaces stay consistent.

## Benchmarks

Latest baseline: Vigil accepted 100,000 log events through the public API with 32 parallel ingest workers and no event loss. Batched SQLite indexing made all 100k logs queryable after about 20.52s total. See [benchmark.md](benchmark.md) for the full readout.

Run a local isolated benchmark:

```sh
make bench ARGS="-events 5000 -concurrency 16 -query-runs 25"
```

## Repository Layout

- `cmd/vigil` - binary entrypoint
- `internal/` - backend application code
- `ui/` - React + TypeScript + Vite frontend
- `web/dist` - built frontend assets
- `docs/` - usage and operations docs
- `test/` - integration, e2e, and fixtures
