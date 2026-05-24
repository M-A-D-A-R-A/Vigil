# Vigil TypeScript SDK

Send logs, traces, and metrics to a running Vigil server from server-side JavaScript or TypeScript.

This first TypeScript SDK is intentionally server-side only. Do not bundle `VIGIL_INGEST_KEY` into browser code. Browser-safe ingest keys and frontend capture helpers will be separate features.

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

Run `vigil init` in your app directory first. It writes the SDK environment values to `.env`:

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
  attrs: { total_tokens: 42, cost_usd: 0.0021 },
});

await vigil.metric("queue.depth", {
  value: 7,
  unit: "count",
  attrs: { queue: "jobs" },
});
```

Use `new VigilClient({ baseUrl, projectId, ingestKey })` if you do not want to configure through environment variables.

## Query

```ts
const recentErrors = await vigil.logs({ level: "error", limit: 20 });
const traces = await vigil.traces({ limit: 20 });
const stats = await vigil.stats();
const health = await vigil.health();
```

Query calls default to the client's `projectId` when one is configured.

## Browser Note

This package uses the private ingest key and is meant for server-side runtimes such as Node, Bun, workers, framework server routes, and backend jobs.

For frontend/browser telemetry, Vigil needs browser-safe ingest keys with origin allowlists, rate limits, and CORS. Do not expose `VIGIL_INGEST_KEY` in a browser bundle.
