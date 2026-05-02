# API

Import-ready Postman collection:

- [docs/postman/vigil-v1.postman_collection.json](/Users/nandoriy/Documents/aiprojects/vigil/vigil/docs/postman/vigil-v1.postman_collection.json)

Single-file curl reference:

- [docs/postman/vigil-v1.curl.md](/Users/nandoriy/Documents/aiprojects/vigil/vigil/docs/postman/vigil-v1.curl.md)

## Health

`GET /api/health`

Returns service health plus:

- indexing sync state
- retention mode and last sweep result

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
- `page`
- `limit`

Responses can include:

- `warnings[]` when the requested page size is capped
- `warnings[]` when more matching results exist beyond the current page

`GET /api/traces`

Returns grouped trace timelines by `trace_id`, with the same `warnings[]` behavior for capped or partial paginated results.

`GET /api/stats`

Returns lightweight summaries built from indexed events.
