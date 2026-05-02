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
