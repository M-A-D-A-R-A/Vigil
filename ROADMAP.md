# Roadmap

Vigil is early alpha. The goal is a small, local-first observability box for side projects: one binary, project-scoped ingest keys, a useful UI, and a CLI that makes logs, traces, and lightweight metrics easy to inspect without running a large stack.

GitHub Issues should be the source of truth for planned work. This roadmap is directional: it explains the shape of the project and the order of bets.

## Current Alpha

Vigil already has:

- single-binary Go server with embedded UI
- project creation and project-scoped ingest keys
- browser-safe ingest keys with origin allowlists and browser-only ingest
- raw append-only NDJSON storage as the source of truth
- SQLite read models for logs, traces, stats, and simple URL-filtered search
- async indexing with rebuild from raw storage
- log, trace, and metric event envelope
- retention sweep with read-model rebuild
- Python SDK
- install script and GitHub release workflow
- benchmarks and smoke tests

## Public Alpha Focus

These are the features that should make Vigil feel useful and distinct for early public users.

### Query Engine

Add a small structured query engine as a separate package that can power the UI, CLI, benchmarks, and future debugging tools.

Vigil does not have this yet. The current `internal/query` package only parses HTTP query parameters into filters for the existing API.

Initial syntax:

```txt
level = "error" && timestamp > now() - 6h
source = "api" && attrs.route = "/login"
name ~= "checkout"
trace_id = "trace_..."
```

The first implementation should compile queries into the existing SQLite read-model path. The parser should stay independent from HTTP handlers and return parse errors with spans and suggestions.

### Live Tail

Add SSE-based live tail for the UI and CLI using an in-memory `TailHub`.

- Publish to `TailHub` only after raw NDJSON append succeeds.
- Keep SQLite indexing async.
- Stream with `GET /api/logs/tail?...` and `Content-Type: text/event-stream`.
- Support heartbeat pings and reconnects.
- Give each subscriber a buffered channel and compiled query matcher.
- Support `after=<cursor>` / `Last-Event-ID` catch-up by backfilling missed events before streaming new ones.
- Track active subscribers, published events, dropped tail events, and disconnects.

### CLI Logs

Expand the CLI from setup commands into daily debugging commands:

```sh
vigil logs --since 1h
vigil logs --level error
vigil logs --query 'level = "error" && source = "api"'
vigil logs --tail
vigil logs --json
```

### SDKs And Ingestion

- Add browser capture helpers on top of browser-safe ingest keys.
- Add file, stdin, JSON-log, and logfmt ingestion paths.
- Add schema presets for app logs, HTTP access logs, worker jobs, LLM / agent traces, and metric events.

### Safety And Debuggability

- Add configurable redaction at ingest for tokens, API keys, cookies, auth headers, emails, and other sensitive values.
- Expose ingest and indexing visibility: queue depth, enqueue drops, rebuild pending/running state, indexing lag, ingest rate, and accurate `indexed_async`.
- Add log context around a selected event: events before / after, same trace, same request ID, and same source window.

## Later

- OpenTelemetry / OTLP ingest.
- Field sidebar based on discovered `attrs` and `body` keys.
- Histogram for ingest volume and event volume.
- Saved views, saved filtered links, and built-in saved query templates.
- Retention rules by level, kind, and source.
- Compressed closed raw segments, such as `.ndjson.zst` or `.ndjson.gz`.
- Generated Vector config for users who already run Vector.
- Focused SQLite indexes or extracted columns for common filters such as level, source, name, request ID, route, status, and model.
- Larger benchmark profiles for 500k and 1M events.
- Evidence-first `vigil ask` layer for natural-language questions over logs, traces, stats, and event context.

## Later If Vigil Expands

- Auth and multi-user RBAC.
- Teams and sources abstractions.
- Alerts and webhooks.
- Query cancellation and export jobs.
- Provisioning flows.
- Model-provider configuration for the `vigil ask` layer.

## Not In Scope Right Now

- ClickHouse source management.
- Schema-agnostic external table management.
- SQL-first querying as the primary user interface.
- OIDC and full user management.
