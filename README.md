# Vigil

Vigil is a local-first observability box for side projects. It gives you a built-in UI for logs, traces, and stats, stores data locally, and uses project-scoped ingest keys so each app can send events without a larger observability stack.

> Vigil is early alpha. It is designed first for local and trusted-network use, not internet-exposed production deployments.

## Project Status

- [Roadmap](ROADMAP.md) - where the project is going
- [Contributing](CONTRIBUTING.md) - local setup and contribution guidance
- [Security](SECURITY.md) - how to report vulnerabilities and current security notes

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

Apps that already use OpenTelemetry can also send OTLP/HTTP protobuf payloads to `/v1/logs`, `/v1/traces`, and `/v1/metrics` with the same Vigil ingest key. Native JSON ingest remains the easiest manual path.

Example OpenTelemetry exporter environment:

```env
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:8080
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer%20vigil_...
OTEL_SERVICE_NAME=my-app
```

## Using The CLI

- `vigil` - start the server
- `vigil serve` - explicitly start the server
- `vigil init [-project NAME]` - create or select a project and write app env values
- `vigil status` - show the active server, project, key state, and health
- `vigil projects` - list projects and mark the active one
- `vigil use PROJECT_ID_OR_NAME` - switch active project
- `vigil key rotate` - create and store a fresh ingest key for the active project
- `vigil ingest-command` - print a ready-to-run curl command
- `vigil logs` - inspect recent logs for the active project

Useful log commands:

```sh
vigil logs --since 1h
vigil logs --errors --q checkout
vigil logs --query 'level = "error" && source = "api"'
vigil logs --stats
vigil logs --fields
vigil logs --json
vigil logs --tail
```

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

## Python SDK

The Python SDK is published on PyPI as `vigil-observability`. It reads the env values from `vigil init` and sends events to Vigil:

```sh
pip install vigil-observability
```

For Python apps that already use OpenTelemetry:

```sh
pip install "vigil-observability[otel]"
```

Package page: <https://pypi.org/project/vigil-observability/>

```python
from vigil_sdk import VigilClient

vigil = VigilClient.from_env()
vigil.log("request.completed", message="request completed", attrs={"route": "/health"})
vigil.trace("llm.completed", trace_id="trace-123", attrs={"total_tokens": 42})
vigil.metric("queue.depth", value=7, unit="count", attrs={"queue": "jobs"})
```

OpenTelemetry helper:

```python
from vigil_sdk import configure_vigil_otel

configure_vigil_otel(service_name="my-app")
```

## TypeScript SDK

The TypeScript SDK lives in `sdk/typescript`. Server-side JavaScript/TypeScript runtimes use the private-key `VigilClient`:

```ts
import { VigilClient } from "vigil-observability";

const vigil = VigilClient.fromEnv();

await vigil.log("request.completed", {
  message: "request completed",
  attrs: { route: "/health" },
});

await vigil.trace("llm.completed", {
  traceId: "trace-123",
  attrs: { total_tokens: 42 },
});

await vigil.metric("queue.depth", {
  value: 7,
  unit: "count",
  attrs: { queue: "jobs" },
});
```

Do not bundle `VIGIL_INGEST_KEY` into browser code. Browser apps should use a browser-safe ingest key with `BrowserVigilClient` or `startVigilBrowserCapture`:

```ts
import { startVigilBrowserCapture } from "vigil-observability";

startVigilBrowserCapture({
  baseUrl: "http://localhost:8080",
  browserIngestKey: import.meta.env.VITE_VIGIL_BROWSER_INGEST_KEY,
});
```

The capture helper records safe summaries for page views, route changes, `console.error`, uncaught errors, unhandled promise rejections, and failed `fetch` calls. It does not capture cookies, local/session storage, auth headers, request bodies, response bodies, password fields, full DOM, screenshots, or HAR files.

## Browser Ingest Keys

Browser ingest keys are public, project-scoped, ingest-only keys for frontend telemetry. They are separate from private server ingest keys, require exact origin allowlists, and can only write to `POST /api/browser/ingest`.

Browser keys do not secure the whole Vigil server. The current project, query, and key-management APIs are still intended for local-admin/trusted-network use until Vigil grows auth and RBAC.

Create one for a project:

```sh
curl -X POST http://localhost:8080/api/projects/proj_.../browser-keys \
  -H 'Content-Type: application/json' \
  -d '{"name":"local web","allowed_origins":["http://localhost:3000"]}'
```

The response returns `browser_ingest_key` once. Use it from browser code with an allowed `Origin`; the browser endpoint infers the project from the key, so `project_id` may be omitted from the event body.

Send a browser event:

```js
await fetch("http://localhost:8080/api/browser/ingest", {
  method: "POST",
  headers: {
    Authorization: `Bearer ${VIGIL_BROWSER_INGEST_KEY}`,
    "Content-Type": "application/json",
  },
  body: JSON.stringify({
    schema_version: 1,
    kind: "log",
    ts: new Date().toISOString(),
    source: "browser",
    level: "error",
    name: "frontend.error",
    attrs: { path: location.pathname },
    body: { message: "client error" },
  }),
});
```

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

Vigil redacts obvious secrets before raw NDJSON append by default. This covers sensitive key names such as `password`, `token`, `authorization`, and `cookie`, plus common secret-looking values such as bearer tokens, JWTs, provider keys, connection strings with credentials, high-entropy strings, and emails. Set `VIGIL_REDACTION_ENABLED=false` only for local debugging where storing raw secrets is acceptable.

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
- `make test-python` - run Python SDK tests
- `make test-typescript` - run TypeScript SDK tests
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
- `sdk/python` - Python SDK package
- `sdk/typescript` - TypeScript SDK package
- `web/dist` - built frontend assets
- `docs/` - usage and operations docs
- `test/` - integration, e2e, and fixtures
