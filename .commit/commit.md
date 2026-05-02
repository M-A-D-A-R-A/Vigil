# Vigil Commit Log

This file tracks working decisions, repo state, and session notes for Vigil.

This copy lives inside the git-initialized `vigil/` repo so it can be versioned with the codebase.

## Project Locations

- Planning docs root: `/Users/nandoriy/Documents/aiprojects/vigil`
- App repo root: `/Users/nandoriy/Documents/aiprojects/vigil/vigil`

## Current Repo Status

- Git repo initialized inside `vigil/`
- Monorepo scaffold created
- Go backend scaffolded
- React + TypeScript + Vite UI scaffolded
- Basic docs added inside repo under `vigil/docs/`
- V1 backend APIs implemented for projects, ingest, logs, traces, and stats
- Raw append and SQLite indexing working end to end
- Frontend now talks to the real APIs
- Retention engine implemented with default-off config and dry-run support
- Explorer UI now has light/dark theme switching, a table-first logs view, and an advanced filter drawer
- Postman collection and curl reference added for local API testing
- Filter behavior now has seeded integration coverage

## Locked Decisions

- Backend language: Go
- Frontend: React + TypeScript + Vite
- UI package manager: Bun
- Query/index store: one SQLite database
- Search: SQLite FTS5
- Source of truth: append-only raw files
- Runtime raw storage layout: `project -> day -> 10MB segment`
- Release shape: one binary with built UI assets
- SDKs: separate from v1 server repo
- Raspberry Pi target: Linux ARM64
- Retention policy shape: day-based raw pruning plus SQLite clear-and-rebuild from remaining raw data
- Search semantics:
  - `q` = broad FTS search across event text
  - `name` = exact event-name match

## Runtime Data Layout

```text
vigil-data/
├── logs/
│   └── {project_id}/
│       └── YYYY-MM-DD/
│           ├── 0001.ndjson
│           ├── 0002.ndjson
│           └── ...
├── index/
│   └── vigil.db
```

## Implemented Scaffold

- `vigil/cmd/vigil/main.go`
- `vigil/internal/app/handler.go`
- `vigil/internal/config/config.go`
- `vigil/ui/`
- `vigil/docs/`
- `vigil/.github/workflows/`
- `vigil/Makefile`

## Implemented V1 Pieces

- project creation and key regeneration
- bearer-authenticated single-event ingest
- canonical event validation and normalization
- project-first raw NDJSON storage
- one SQLite index database
- async log indexing
- log explorer API
- grouped trace timeline API
- lightweight stats API
- polling-based UI for projects, logs, traces, and stats
- one-time plaintext project key display in the UI
- sample data seeder for local testing
- size/build helpers for combined UI + Go artifacts
- Raspberry Pi friendly Linux ARM64 build target
- retention status reporting through health
- capped / partial query warnings in the APIs and UI
- export and shareable filtered explorer URLs
- Postman collection and curl reference docs
- advanced filter coverage in backend integration tests

## Useful Commands

### Local backend

```bash
cd /Users/nandoriy/Documents/aiprojects/vigil/vigil
GOCACHE=$(pwd)/.gocache go run ./cmd/vigil
```

### Local frontend

```bash
cd /Users/nandoriy/Documents/aiprojects/vigil/vigil/ui
bun install
bun run dev
```

### Built local app

```bash
cd /Users/nandoriy/Documents/aiprojects/vigil/vigil
make ui-build
GOCACHE=$(pwd)/.gocache go run ./cmd/vigil
```

### Size checks

```bash
cd /Users/nandoriy/Documents/aiprojects/vigil/vigil
make size
make size-release
make build-linux-arm64
```

## Latest Measured Sizes

- Go binary: about `14.44 MB`
- UI bundle: about `225 KB`
- Combined current size: about `14.66 MB`
- Stripped release binary: not re-measured in this session after the final UI/backend pass
- Linux ARM64 binary: supported via `make build-linux-arm64`

## Next Implementation Work

1. add browser-level smoke / e2e coverage for first run and explorer workflows
2. improve trace view details and span metadata handling
3. add saved views and stronger search ergonomics
4. harden release workflows and packaging
5. decide whether retention should later get a UI admin surface instead of env-only control

## Session Notes

### 2026-05-02

- Monorepo scaffold created inside `vigil/`
- Git was initialized by the user in the repo folder
- Go upgraded to `1.26.2`
- `go.mod` updated to `go 1.26.2`
- Added `make size`
- Added `make size-release`
- Added `make build-linux-arm64`
- Implemented project creation, ingest, logs, traces, and stats APIs
- Added async raw-to-SQLite indexing
- Added frontend UI for first-run setup and observability views
- Updated planning docs to match project-first raw storage and one SQLite DB
- Added project-first raw storage under `vigil-data/logs/{project_id}/{YYYY-MM-DD}/{0001..}.ndjson`
- Added one SQLite DB at `vigil-data/index/vigil.db`
- Added seeded sample-data generator at `cmd/vigil-seed`
- Added query warnings for capped page size and partial paginated results
- Implemented retention config:
  - `VIGIL_RETENTION_ENABLED`
  - `VIGIL_RETENTION_DAYS`
  - `VIGIL_RETENTION_SWEEP_INTERVAL`
  - `VIGIL_RETENTION_DRY_RUN`
- Implemented retention engine behavior:
  - prune expired raw UTC day folders
  - rebuild SQLite read models from remaining NDJSON
  - expose retention state in `/api/health`
- Added retention tests and raw-prune tests
- Added Postman import collection and single-file curl reference under `docs/postman/`
- Restyled the explorer toward a denser operator UI
- Added light/dark theme toggle with saved preference
- Moved common search controls to the top toolbar
- Converted advanced filters into an overlay drawer
- Kept only one visible filter button in the project explorer
- Added debounced full-text search for logs
- Added seeded integration coverage proving:
  - `kind` filter works
  - `level` filter works
  - `name` is exact match
  - `q` is broad text search
  - time range filtering works
  - pagination works

## Suggested Commit

`feat: ship Vigil v1 observability flow with retention, explorer polish, and test coverage`
