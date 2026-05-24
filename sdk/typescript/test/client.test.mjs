import assert from "node:assert/strict";
import test from "node:test";

import {
  BrowserVigilClient,
  NoopVigilClient,
  NoopBrowserVigilClient,
  VigilClient,
  VigilConfigError,
  VigilHTTPError,
  childTraceContext,
  continueTraceContext,
  createTraceContext,
  formatTraceparent,
  parseTraceparent,
  startVigilBrowserCapture,
  traceparentHeaders,
} from "../dist/index.js";

function fakeTransport(status = 202, payload = {}) {
  const calls = [];
  const defaultPayload = {
    event_id: "evt_123",
    received_at: "2026-05-10T00:00:00Z",
    indexed_async: true,
  };
  const transport = async (request) => {
    calls.push({
      ...request,
      headers: { ...request.headers },
    });
    return {
      status,
      body: JSON.stringify(Object.keys(payload).length > 0 ? payload : defaultPayload),
    };
  };
  transport.calls = calls;
  return transport;
}

test("fromEnv reads vigil init values", () => {
  const transport = fakeTransport();
  const client = VigilClient.fromEnv({
    env: {
      VIGIL_BASE_URL: "http://localhost:8080/",
      VIGIL_PROJECT_ID: "proj_123",
      VIGIL_INGEST_KEY: "vigil_123",
    },
    transport,
  });

  assert.equal(client.baseUrl, "http://localhost:8080");
  assert.equal(client.projectId, "proj_123");
  assert.equal(client.ingestKey, "vigil_123");
});

test("fromEnv requires project and key", () => {
  assert.throws(
    () => VigilClient.fromEnv({ env: { VIGIL_BASE_URL: "http://localhost:8080" } }),
    (error) => error instanceof VigilConfigError
      && error.message.includes("VIGIL_PROJECT_ID, VIGIL_INGEST_KEY"),
  );
});

test("fromEnv optional returns noop client", async () => {
  const client = VigilClient.fromEnv({
    env: { VIGIL_BASE_URL: "http://localhost:8080" },
    optional: true,
  });

  assert.ok(client instanceof NoopVigilClient);
  const result = await client.log("app.started");
  assert.deepEqual(result, { eventId: "", receivedAt: "", indexedAsync: false });
  assert.equal((await client.health()).status, "disabled");
  assert.equal((await client.logs()).total, 0);
});

test("log sends ingest envelope", async () => {
  const transport = fakeTransport();
  const client = new VigilClient({
    baseUrl: "http://vigil.local",
    projectId: "proj_123",
    ingestKey: "vigil_123",
    transport,
  });

  const result = await client.log("request.completed", {
    message: "ok",
    attrs: { route: "/health" },
    ts: new Date("2026-05-10T01:02:03Z"),
  });

  assert.equal(result.eventId, "evt_123");
  const call = transport.calls[0];
  assert.equal(call.method, "POST");
  assert.equal(call.url, "http://vigil.local/api/ingest");
  assert.equal(call.headers.Authorization, "Bearer vigil_123");
  assert.equal(call.headers["Content-Type"], "application/json");
  assert.deepEqual(JSON.parse(call.body), {
    schema_version: 1,
    project_id: "proj_123",
    kind: "log",
    ts: "2026-05-10T01:02:03.000Z",
    source: "typescript-sdk",
    name: "request.completed",
    attrs: { route: "/health" },
    body: { message: "ok" },
    level: "info",
  });
});

test("trace sends trace fields", async () => {
  const transport = fakeTransport();
  const client = new VigilClient({ projectId: "proj_123", ingestKey: "vigil_123", transport });

  await client.trace("llm.completed", { traceId: "trace-1", spanId: "span-1", parentSpanId: "span-root" });

  const envelope = JSON.parse(transport.calls[0].body);
  assert.equal(envelope.kind, "trace");
  assert.equal(envelope.trace_id, "trace-1");
  assert.equal(envelope.span_id, "span-1");
  assert.equal(envelope.parent_span_id, "span-root");
});

test("traceparent helpers parse, format, and create child contexts", () => {
  const parent = parseTraceparent("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01");

  assert.deepEqual(parent, {
    traceId: "4bf92f3577b34da6a3ce929d0e0e4736",
    spanId: "00f067aa0ba902b7",
    sampled: true,
  });

  assert.equal(formatTraceparent(parent), "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01");
  assert.deepEqual(traceparentHeaders(parent), {
    traceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
  });

  const child = childTraceContext(parent);
  assert.equal(child.traceId, parent.traceId);
  assert.equal(child.parentSpanId, parent.spanId);
  assert.match(child.spanId, /^[0-9a-f]{16}$/);

  const continued = continueTraceContext("00-4bf92f3577b34da6a3ce929d0e0e4736-1111111111111111-00");
  assert.equal(continued.traceId, "4bf92f3577b34da6a3ce929d0e0e4736");
  assert.equal(continued.parentSpanId, "1111111111111111");
  assert.equal(parseTraceparent("00-00000000000000000000000000000000-00f067aa0ba902b7-01"), undefined);

  const fresh = createTraceContext();
  assert.match(fresh.traceId, /^[0-9a-f]{32}$/);
  assert.match(fresh.spanId, /^[0-9a-f]{16}$/);
});

test("metric puts value and unit in attrs", async () => {
  const transport = fakeTransport();
  const client = new VigilClient({ projectId: "proj_123", ingestKey: "vigil_123", transport });

  await client.metric("queue.depth", { value: 7, unit: "count", attrs: { queue: "jobs" } });

  const envelope = JSON.parse(transport.calls[0].body);
  assert.equal(envelope.kind, "metric");
  assert.deepEqual(envelope.attrs, { queue: "jobs", value: 7, unit: "count" });
});

test("createProject returns project result", async () => {
  const transport = fakeTransport(201, {
    project: { id: "proj_123", name: "demo" },
    ingest_key: "vigil_123",
  });
  const client = new VigilClient({ transport });

  const result = await client.createProject("demo");

  assert.deepEqual(result.project, { id: "proj_123", name: "demo" });
  assert.equal(result.ingestKey, "vigil_123");
  assert.deepEqual(JSON.parse(transport.calls[0].body), { name: "demo" });
});

test("query defaults to client project id", async () => {
  const transport = fakeTransport(200, { items: [] });
  const client = new VigilClient({ projectId: "proj_123", ingestKey: "vigil_123", transport });

  await client.logs({ limit: 10 });

  assert.equal(transport.calls[0].method, "GET");
  assert.match(transport.calls[0].url, /\/api\/logs\?/);
  assert.match(transport.calls[0].url, /project_id=proj_123/);
  assert.match(transport.calls[0].url, /limit=10/);
});

test("http errors include server message", async () => {
  const transport = fakeTransport(401, { error: "authorization header is required" });
  const client = new VigilClient({ projectId: "proj_123", ingestKey: "vigil_123", transport });

  await assert.rejects(
    () => client.log("request.completed"),
    (error) => error instanceof VigilHTTPError
      && error.statusCode === 401
      && error.message.includes("authorization header is required"),
  );
});

test("browser fromEnv reads public browser ingest key", () => {
  const transport = fakeTransport();
  const client = BrowserVigilClient.fromEnv({
    env: {
      VIGIL_BASE_URL: "http://localhost:8080/",
      VIGIL_BROWSER_INGEST_KEY: "vigil_browser_123",
    },
    transport,
  });

  assert.equal(client.baseUrl, "http://localhost:8080");
  assert.equal(client.browserIngestKey, "vigil_browser_123");
});

test("browser fromEnv optional returns noop client", async () => {
  const client = BrowserVigilClient.fromEnv({
    env: { VIGIL_BASE_URL: "http://localhost:8080" },
    optional: true,
  });

  assert.ok(client instanceof NoopBrowserVigilClient);
  const result = await client.log("browser.started");
  assert.deepEqual(result, { eventId: "", receivedAt: "", indexedAsync: false });
});

test("browser ingest sends browser envelope without private project id", async () => {
  const transport = fakeTransport();
  const client = new BrowserVigilClient({
    baseUrl: "http://vigil.local",
    browserIngestKey: "vigil_browser_123",
    transport,
  });

  await client.log("frontend.error", {
    level: "error",
    message: "client exploded",
    attrs: { path: "/checkout" },
    ts: new Date("2026-05-10T01:02:03Z"),
  });

  const call = transport.calls[0];
  assert.equal(call.method, "POST");
  assert.equal(call.url, "http://vigil.local/api/browser/ingest");
  assert.equal(call.headers.Authorization, "Bearer vigil_browser_123");
  const envelope = JSON.parse(call.body);
  assert.equal(envelope.project_id, undefined);
  assert.deepEqual(envelope, {
    schema_version: 1,
    kind: "log",
    ts: "2026-05-10T01:02:03.000Z",
    source: "browser",
    name: "frontend.error",
    attrs: { path: "/checkout" },
    body: { message: "client exploded" },
    level: "error",
  });
});

test("browser capture sends safe page and fetch summaries", async () => {
  const transport = fakeTransport();
  const fetchCalls = [];
  const fakeWindow = {
    location: { origin: "http://app.local", pathname: "/checkout" },
    document: { referrer: "http://app.local/start?token=secret" },
    navigator: { language: "en-US", userAgent: "TestBrowser/1.0" },
    innerWidth: 1280,
    innerHeight: 720,
    console: { error() {} },
    addEventListener() {},
    removeEventListener() {},
    history: {
      pushState() {},
      replaceState() {},
    },
    fetch: async (input, init) => {
      fetchCalls.push({ input, init });
      return { ok: false, status: 503, text: async () => "" };
    },
  };

  const handle = startVigilBrowserCapture({
    baseUrl: "http://vigil.local",
    browserIngestKey: "vigil_browser_123",
    transport,
    window: fakeWindow,
    captureConsoleErrors: false,
    captureErrors: false,
    captureUnhandledRejections: false,
    captureRouteChanges: false,
  });
  await new Promise((resolve) => setTimeout(resolve, 0));
  await fakeWindow.fetch("http://api.local/payments?secret=abc", { method: "POST" });
  await new Promise((resolve) => setTimeout(resolve, 0));
  handle.stop();

  const bodies = transport.calls.map((call) => JSON.parse(call.body));
  assert.equal(bodies[0].name, "browser.page_view");
  assert.equal(bodies[0].attrs.path, "/checkout");
  assert.equal(bodies[0].attrs.referrer, "/start");

  const fetchEvent = bodies.find((body) => body.name === "browser.fetch_failed");
  assert.ok(fetchEvent);
  assert.match(fetchEvent.trace_id, /^[0-9a-f]{32}$/);
  assert.match(fetchEvent.span_id, /^[0-9a-f]{16}$/);
  assert.equal(fetchEvent.attrs.method, "POST");
  assert.equal(fetchEvent.attrs.path, "/payments");
  assert.equal(fetchEvent.attrs.status, 503);
  assert.equal(fetchEvent.attrs.duration_ms >= 0, true);
  assert.match(fetchCalls[0].init.headers.get("traceparent"), /^00-[0-9a-f]{32}-[0-9a-f]{16}-01$/);
});
