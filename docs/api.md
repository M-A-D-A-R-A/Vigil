# API

Import-ready Postman collection:

- [docs/postman/vigil-v1.postman_collection.json](/Users/nandoriy/Documents/aiprojects/vigil/vigil/docs/postman/vigil-v1.postman_collection.json)

Single-file curl reference:

- [docs/postman/vigil-v1.curl.md](/Users/nandoriy/Documents/aiprojects/vigil/vigil/docs/postman/vigil-v1.curl.md)

## Health

`GET /api/health`

Returns service health plus:

- indexing sync state
- ingest visibility: accepted count and rolling ingest rate
- index worker visibility: queue depth, enqueue drops, and rebuild pending/running state
- retention mode and last sweep result

Example health shape:

```json
{
  "status": "ok",
  "app": "vigil",
  "sync": {
    "latest_ingested_at": "2026-05-19T12:00:01Z",
    "latest_indexed_at": "2026-05-19T12:00:00Z",
    "last_rebuild_at": "2026-05-19T11:59:00Z",
    "stale": true,
    "indexing_lag_seconds": 1,
    "indexing_lag": "1s"
  },
  "ingest": {
    "total_accepted": 120,
    "rate_window_seconds": 60,
    "recent_events": 12,
    "events_per_second": 0.2,
    "events_per_minute": 12
  },
  "index": {
    "queue_depth": 2,
    "queue_capacity": 256,
    "enqueue_drops": 0,
    "rebuild_pending": false,
    "rebuild_running": false,
    "rebuild_queue_capacity": 1
  }
}
```

## Projects

`POST /api/projects`

Create a project and return:

- project metadata
- plaintext ingest key shown once

Request body:

```json
{
  "name": "my-app"
}
```

`GET /api/projects`

List projects without exposing stored ingest keys.

`POST /api/projects/{id}/keys/regenerate`

Rotate the ingest key for a project and return the new plaintext key once.

## Ingest

Vigil's native JSON ingest remains the simplest first-run path.

`POST /api/ingest`

Headers:

- `Authorization: Bearer <ingest_key>`
- `Content-Type: application/json`

Body:

```json
{
  "schema_version": 1,
  "project_id": "proj_123",
  "kind": "log",
  "ts": "2026-05-02T12:00:00Z",
  "source": "api",
  "name": "request.completed",
  "level": "info",
  "attrs": {
    "route": "/users"
  },
  "body": {
    "message": "request completed"
  }
}
```

### OpenTelemetry OTLP/HTTP

Vigil can also receive OTLP/HTTP protobuf payloads from standard OpenTelemetry exporters:

- `POST /v1/logs`
- `POST /v1/traces`
- `POST /v1/metrics`

Headers:

- `Authorization: Bearer <ingest_key>`
- `Content-Type: application/x-protobuf`

Most OpenTelemetry SDKs/exporters can be pointed at Vigil with environment variables:

```env
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:8080
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer vigil_...
OTEL_SERVICE_NAME=my-app
```

The authenticated ingest key selects the Vigil project. OTLP resource field `service.name` becomes the Vigil `source`; OTLP logs become `kind=log`, spans become `kind=trace`, and metric data points become `kind=metric`. OpenTelemetry resource, scope, and signal attributes are preserved under `attrs.otel`, while signal-level attributes are also promoted into top-level `attrs` for normal Vigil filtering and search.

## Query APIs

`GET /api/logs`

Supported query params:

- `project_id`
- `from`
- `to`
- `kind`
- `level`
- `name`
- `q`
- `query`
- `page`
- `limit`

`q` is broad full-text search across event name, source, level, attrs, and body text.

`query` is structured search. Initial examples:

```txt
level = "error" && timestamp > now() - 6h
source = "api" && attrs.route = "/login"
name ~= "checkout"
trace_id = "trace_123"
```

Supported structured fields are `kind`, `level`, `source`, `name`, `trace_id`, `span_id`, `timestamp` / `ts`, plus one-level JSON fields such as `attrs.route` and `body.message`.

Responses can include:

- `warnings[]` when the requested page size is capped
- `warnings[]` when more matching results exist beyond the current page

`GET /api/logs/tail`

Streams matching log events with Server-Sent Events:

```http
GET /api/logs/tail?project_id=proj_123&level=error&query=source%20%3D%20%22api%22
Accept: text/event-stream
```

Events use the stored event ID as the SSE `id`, `event: log`, and a JSON stored event payload:

```txt
id: evt_123
event: log
data: {"event_id":"evt_123","kind":"log","name":"request.failed"}
```

Reconnect catch-up is supported with either `after=<event_id>` or the standard `Last-Event-ID` header. Catch-up reads from the SQLite read model before streaming newly published events. If `to` is omitted, live tail treats the upper time bound as open-ended so future events can match. Heartbeat pings are sent as `event: ping`.

`GET /api/traces`

Returns grouped trace timelines by `trace_id`, with the same `warnings[]` behavior for capped or partial paginated results.

`GET /api/stats`

Returns lightweight summaries built from indexed events.
