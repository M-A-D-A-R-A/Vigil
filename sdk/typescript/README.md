# Vigil TypeScript SDK

Send logs, traces, and metrics to a running Vigil server from server-side JavaScript, TypeScript, and browser apps.

Server code should use `VigilClient` with the private `VIGIL_INGEST_KEY`. Browser code must use `BrowserVigilClient` or `startVigilBrowserCapture` with a public browser ingest key. Do not bundle `VIGIL_INGEST_KEY` into browser code.

## Install

From this repository during development:

```sh
cd sdk/typescript
npm install
npm test
```

When published, install the package in your app:

```sh
npm install vigil-observability
```

## Configure

Run `vigil init` in your app directory first. It writes the server-side SDK environment values to `.env`:

```env
VIGIL_BASE_URL=http://localhost:8080
VIGIL_PROJECT_ID=proj_...
VIGIL_INGEST_KEY=vigil_...
```

Load those values into your server process, then create a client:

```ts
import { VigilClient } from "vigil-observability";

const vigil = VigilClient.fromEnv();
```

For optional instrumentation, use `optional: true`. This returns a no-op client when `VIGIL_PROJECT_ID` or `VIGIL_INGEST_KEY` is missing, so app code can keep calling `log`, `trace`, and `metric` without scattered configuration checks:

```ts
const vigil = VigilClient.fromEnv({ optional: true });
await vigil.log("app.started", { message: "app started" });
```

## Send Events

```ts
await vigil.log("request.completed", {
  message: "request completed",
  attrs: { route: "/health" },
});

await vigil.trace("llm.completed", {
  traceId: "trace-123",
  spanId: "span-1",
  parentSpanId: "span-root",
  attrs: { total_tokens: 42, cost_usd: 0.0021 },
});

await vigil.metric("queue.depth", {
  value: 7,
  unit: "count",
  attrs: { queue: "jobs" },
});
```

Use `new VigilClient({ baseUrl, projectId, ingestKey })` if you do not want to configure through environment variables.

## Browser Capture

Create a browser-safe ingest key on the Vigil server first:

```sh
curl -X POST http://localhost:8080/api/projects/proj_.../browser-keys \
  -H 'Content-Type: application/json' \
  -d '{"name":"local web","allowed_origins":["http://localhost:3000"]}'
```

Expose only that returned `browser_ingest_key` to frontend code, for example as `VIGIL_BROWSER_INGEST_KEY`.

Manual browser event:

```ts
import { BrowserVigilClient } from "vigil-observability";

const vigil = new BrowserVigilClient({
  baseUrl: "http://localhost:8080",
  browserIngestKey: import.meta.env.VITE_VIGIL_BROWSER_INGEST_KEY,
});

await vigil.log("frontend.error", {
  level: "error",
  message: "client error",
  attrs: { path: location.pathname },
});
```

Automatic safe defaults:

```ts
import { startVigilBrowserCapture } from "vigil-observability";

const capture = startVigilBrowserCapture({
  baseUrl: "http://localhost:8080",
  browserIngestKey: import.meta.env.VITE_VIGIL_BROWSER_INGEST_KEY,
});
```

The capture helper records safe summaries for page views, route changes, `console.error`, uncaught errors, unhandled promise rejections, and `fetch` calls. By default it attaches W3C `traceparent` headers to outgoing `fetch` requests and records request spans with `trace_id`, `span_id`, and `parent_span_id`. It does not capture cookies, local/session storage, auth headers, request bodies, response bodies, password fields, full DOM, screenshots, or HAR files.

Disable fetch span capture or header propagation when needed:

```ts
startVigilBrowserCapture({
  baseUrl: "http://localhost:8080",
  browserIngestKey: import.meta.env.VITE_VIGIL_BROWSER_INGEST_KEY,
  captureFetchSpans: false,
  propagateTraceHeaders: false,
});
```

## Trace Context

Use the W3C helper functions to continue a browser trace on the server and forward it to downstream services:

```ts
import { VigilClient, continueTraceContext, parseTraceparent, traceparentHeaders } from "vigil-observability";

const vigil = VigilClient.fromEnv();

export async function POST(request: Request) {
  const span = continueTraceContext(parseTraceparent(request.headers.get("traceparent")));

  await vigil.log("api.checkout.started", {
    traceId: span.traceId,
    spanId: span.spanId,
    parentSpanId: span.parentSpanId,
    attrs: { route: "/api/checkout" },
  });

  await fetch("http://worker.local/jobs", {
    method: "POST",
    headers: traceparentHeaders(span),
  });
}
```

Query by `trace_id` in Vigil to see the browser event, API log, downstream worker, and any later DB/ingest events in one flow. Framework-specific fields can stay in `attrs`.

## Query

```ts
const recentErrors = await vigil.logs({ level: "error", limit: 20 });
const traces = await vigil.traces({ limit: 20 });
const stats = await vigil.stats();
const health = await vigil.health();
```

Query calls default to the client's `projectId` when one is configured.

## Browser Security Notes

Browser ingest keys are public, project-scoped, ingest-only keys. They require exact origin allowlists and are meant for browser telemetry only. They do not secure the rest of the Vigil server; project, query, and key-management APIs are still local-admin/trusted-network APIs until Vigil adds auth/RBAC.
