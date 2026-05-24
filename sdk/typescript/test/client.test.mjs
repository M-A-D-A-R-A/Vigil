import assert from "node:assert/strict";
import test from "node:test";

import {
  NoopVigilClient,
  VigilClient,
  VigilConfigError,
  VigilHTTPError,
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

  await client.trace("llm.completed", { traceId: "trace-1", spanId: "span-1" });

  const envelope = JSON.parse(transport.calls[0].body);
  assert.equal(envelope.kind, "trace");
  assert.equal(envelope.trace_id, "trace-1");
  assert.equal(envelope.span_id, "span-1");
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
