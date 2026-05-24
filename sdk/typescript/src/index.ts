export type JsonPrimitive = string | number | boolean | null;
export type JsonValue = JsonPrimitive | JsonValue[] | { [key: string]: JsonValue | undefined };
export type JsonObject = { [key: string]: JsonValue | undefined };

export type EventKind = "log" | "trace" | "metric" | (string & {});

export interface TransportRequest {
  method: string;
  url: string;
  headers: Record<string, string>;
  body?: string | undefined;
  timeoutMs: number;
}

export interface TransportResponse {
  status: number;
  body: string;
}

export type Transport = (request: TransportRequest) => Promise<TransportResponse> | TransportResponse;

export interface VigilClientOptions {
  baseUrl?: string | undefined;
  projectId?: string | undefined;
  ingestKey?: string | undefined;
  source?: string | undefined;
  timeoutMs?: number | undefined;
  transport?: Transport | undefined;
}

export interface VigilEnv {
  VIGIL_BASE_URL?: string;
  VIGIL_PROJECT_ID?: string;
  VIGIL_INGEST_KEY?: string;
  VIGIL_BROWSER_INGEST_KEY?: string;
  VIGIL_SOURCE?: string;
  [key: string]: string | undefined;
}

export interface FromEnvOptions {
  env?: VigilEnv | undefined;
  source?: string | undefined;
  timeoutMs?: number | undefined;
  transport?: Transport | undefined;
  optional?: boolean | undefined;
}

export interface IngestResult {
  eventId: string;
  receivedAt: string;
  indexedAsync: boolean;
}

export interface ProjectResult {
  project: Record<string, unknown>;
  ingestKey: string;
}

export interface IngestOptions {
  kind: EventKind;
  name: string;
  attrs?: JsonObject | undefined;
  body?: JsonValue | undefined;
  ts?: Date | string | undefined;
  source?: string | undefined;
  level?: string | undefined;
  traceId?: string | undefined;
  spanId?: string | undefined;
  parentSpanId?: string | undefined;
}

export interface LogOptions {
  message?: string | undefined;
  level?: string | undefined;
  attrs?: JsonObject | undefined;
  body?: JsonValue | undefined;
  ts?: Date | string | undefined;
  source?: string | undefined;
  traceId?: string | undefined;
  spanId?: string | undefined;
  parentSpanId?: string | undefined;
}

export interface TraceOptions {
  traceId: string;
  spanId?: string | undefined;
  parentSpanId?: string | undefined;
  level?: string | undefined;
  attrs?: JsonObject | undefined;
  body?: JsonValue | undefined;
  ts?: Date | string | undefined;
  source?: string | undefined;
}

export interface MetricOptions {
  value: number;
  unit?: string | undefined;
  attrs?: JsonObject | undefined;
  body?: JsonValue | undefined;
  ts?: Date | string | undefined;
  source?: string | undefined;
}

export interface BrowserVigilClientOptions {
  baseUrl?: string | undefined;
  browserIngestKey?: string | undefined;
  source?: string | undefined;
  timeoutMs?: number | undefined;
  transport?: Transport | undefined;
}

export interface BrowserFromEnvOptions {
  env?: VigilEnv | undefined;
  source?: string | undefined;
  timeoutMs?: number | undefined;
  transport?: Transport | undefined;
  optional?: boolean | undefined;
}

export interface BrowserIngestOptions {
  kind?: EventKind | undefined;
  name: string;
  attrs?: JsonObject | undefined;
  body?: JsonValue | undefined;
  ts?: Date | string | undefined;
  source?: string | undefined;
  level?: string | undefined;
  traceId?: string | undefined;
  spanId?: string | undefined;
  parentSpanId?: string | undefined;
}

export interface BrowserLogOptions {
  message?: string | undefined;
  level?: string | undefined;
  attrs?: JsonObject | undefined;
  body?: JsonValue | undefined;
  ts?: Date | string | undefined;
  source?: string | undefined;
  traceId?: string | undefined;
  spanId?: string | undefined;
  parentSpanId?: string | undefined;
}

export interface BrowserCaptureOptions extends BrowserVigilClientOptions {
  client?: BrowserVigilClient | undefined;
  captureConsoleErrors?: boolean | undefined;
  captureErrors?: boolean | undefined;
  captureUnhandledRejections?: boolean | undefined;
  capturePageViews?: boolean | undefined;
  captureRouteChanges?: boolean | undefined;
  captureFetchSpans?: boolean | undefined;
  captureFetchFailures?: boolean | undefined;
  propagateTraceHeaders?: boolean | undefined;
  baseAttrs?: JsonObject | undefined;
  window?: BrowserWindowLike | undefined;
}

export interface TraceContext {
  traceId: string;
  spanId: string;
  parentSpanId?: string | undefined;
  sampled?: boolean | undefined;
}

export interface BrowserCaptureHandle {
  client: BrowserVigilClient;
  stop(): void;
}

export interface BrowserWindowLike {
  location?: {
    origin?: string | undefined;
    pathname?: string | undefined;
  } | undefined;
  document?: {
    referrer?: string | undefined;
  } | undefined;
  navigator?: {
    language?: string | undefined;
    userAgent?: string | undefined;
  } | undefined;
  innerWidth?: number | undefined;
  innerHeight?: number | undefined;
  console?: {
    error: (...args: unknown[]) => void;
  } | undefined;
  history?: {
    pushState: (...args: any[]) => void;
    replaceState: (...args: any[]) => void;
  } | undefined;
  fetch?: (input: any, init?: any) => Promise<{ ok: boolean; status: number }>;
  addEventListener(type: string, listener: (event: unknown) => void): void;
  removeEventListener(type: string, listener: (event: unknown) => void): void;
}

export type QueryValue = string | number | boolean | Date | null | undefined;
export type QueryParams = Record<string, QueryValue>;

export class VigilError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "VigilError";
  }
}

export class VigilConfigError extends VigilError {
  constructor(message: string) {
    super(message);
    this.name = "VigilConfigError";
  }
}

export class VigilHTTPError extends VigilError {
  readonly statusCode: number;
  readonly body: string;

  constructor(statusCode: number, message: string, body = "") {
    super(message);
    this.name = "VigilHTTPError";
    this.statusCode = statusCode;
    this.body = body;
  }
}

export function createTraceContext(options: { sampled?: boolean | undefined } = {}): TraceContext {
  return {
    traceId: randomHex(16),
    spanId: randomHex(8),
    sampled: options.sampled ?? true,
  };
}

export function childTraceContext(parent: TraceContext | string): TraceContext {
  const parsed = typeof parent === "string" ? parseTraceparent(parent) : parent;
  if (!parsed) {
    return createTraceContext();
  }
  return {
    traceId: parsed.traceId,
    parentSpanId: parsed.spanId,
    spanId: randomHex(8),
    sampled: parsed.sampled ?? true,
  };
}

export function continueTraceContext(parent: TraceContext | string | undefined | null): TraceContext {
  if (!parent) {
    return createTraceContext();
  }
  return childTraceContext(parent);
}

export function parseTraceparent(header: string | undefined | null): TraceContext | undefined {
  const value = clean(header).toLowerCase();
  const match = /^([0-9a-f]{2})-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$/.exec(value);
  if (!match) {
    return undefined;
  }
  const version = match[1] ?? "";
  const traceId = match[2] ?? "";
  const spanId = match[3] ?? "";
  const flags = match[4] ?? "";
  if (version === "ff" || !isValidTraceId(traceId) || !isValidSpanId(spanId)) {
    return undefined;
  }
  return {
    traceId,
    spanId,
    sampled: (parseInt(flags, 16) & 1) === 1,
  };
}

export function formatTraceparent(context: TraceContext): string {
  const traceId = clean(context.traceId).toLowerCase();
  const spanId = clean(context.spanId).toLowerCase();
  if (!isValidTraceId(traceId)) {
    throw new VigilConfigError("traceId must be a non-zero 32-character hex string");
  }
  if (!isValidSpanId(spanId)) {
    throw new VigilConfigError("spanId must be a non-zero 16-character hex string");
  }
  return `00-${traceId}-${spanId}-${context.sampled === false ? "00" : "01"}`;
}

export function traceparentHeaders(context: TraceContext): Record<string, string> {
  return { traceparent: formatTraceparent(context) };
}

export class VigilClient {
  readonly baseUrl: string;
  readonly projectId: string;
  readonly ingestKey: string;
  readonly source: string;
  readonly timeoutMs: number;
  readonly disabled: boolean = false;
  readonly disabledReason: string = "";

  protected readonly transport: Transport;

  constructor(options: VigilClientOptions = {}) {
    this.baseUrl = normalizeBaseUrl(options.baseUrl ?? "http://localhost:8080");
    this.projectId = clean(options.projectId);
    this.ingestKey = clean(options.ingestKey);
    this.source = clean(options.source) || "typescript-sdk";
    this.timeoutMs = options.timeoutMs ?? 10_000;
    this.transport = options.transport ?? fetchTransport;
  }

  static fromEnv(options: FromEnvOptions = {}): VigilClient {
    const env = options.env ?? defaultEnv();
    const baseUrl = env.VIGIL_BASE_URL ?? "http://localhost:8080";
    const projectId = env.VIGIL_PROJECT_ID;
    const ingestKey = env.VIGIL_INGEST_KEY;
    const source = options.source ?? env.VIGIL_SOURCE ?? "typescript-sdk";

    const missing = Object.entries({
      VIGIL_PROJECT_ID: projectId,
      VIGIL_INGEST_KEY: ingestKey,
    })
      .filter(([, value]) => !clean(value))
      .map(([key]) => key);

    if (missing.length > 0) {
      if (options.optional) {
        return new NoopVigilClient({
          baseUrl,
          projectId,
          ingestKey,
          source,
          timeoutMs: options.timeoutMs,
          disabledReason: `missing required environment variables: ${missing.join(", ")}`,
        });
      }
      throw new VigilConfigError(`missing required environment variables: ${missing.join(", ")}`);
    }

    return new VigilClient({
      baseUrl,
      projectId,
      ingestKey,
      source,
      timeoutMs: options.timeoutMs,
      transport: options.transport,
    });
  }

  async health(): Promise<Record<string, unknown>> {
    return this.request("GET", "/api/health");
  }

  async createProject(name: string): Promise<ProjectResult> {
    const payload = await this.request("POST", "/api/projects", { jsonBody: { name } });
    return projectResultFromJson(payload);
  }

  async listProjects(): Promise<Record<string, unknown>[]> {
    const payload = await this.request("GET", "/api/projects");
    const projects = payload["projects"];
    return Array.isArray(projects) ? projects.map((project) => objectFromUnknown(project)) : [];
  }

  async rotateKey(projectId?: string): Promise<ProjectResult> {
    const resolvedProjectId = clean(projectId) || this.projectId;
    if (!resolvedProjectId) {
      throw new VigilConfigError("projectId is required to rotate an ingest key");
    }
    const path = `/api/projects/${encodeURIComponent(resolvedProjectId)}/keys/regenerate`;
    const payload = await this.request("POST", path);
    return projectResultFromJson(payload);
  }

  async ingest(options: IngestOptions): Promise<IngestResult> {
    this.requireIngestConfig();

    const envelope: Record<string, JsonValue | undefined> = {
      schema_version: 1,
      project_id: this.projectId,
      kind: required("kind", options.kind).toLowerCase(),
      ts: formatTimestamp(options.ts),
      source: clean(options.source) || this.source,
      name: required("name", options.name),
      attrs: options.attrs ?? {},
      body: options.body ?? null,
    };
    setIfPresent(envelope, "level", options.level);
    setIfPresent(envelope, "trace_id", options.traceId);
    setIfPresent(envelope, "span_id", options.spanId);
    setIfPresent(envelope, "parent_span_id", options.parentSpanId);

    const payload = await this.request("POST", "/api/ingest", {
      headers: { Authorization: `Bearer ${this.ingestKey}` },
      jsonBody: envelope,
    });
    return ingestResultFromJson(payload);
  }

  async log(name: string, options: LogOptions = {}): Promise<IngestResult> {
    const body = options.body === undefined && options.message !== undefined
      ? { message: options.message }
      : options.body;
    return this.ingest({
      kind: "log",
      name,
      level: options.level ?? "info",
      attrs: options.attrs,
      body,
      ts: options.ts,
      source: options.source,
      traceId: options.traceId,
      spanId: options.spanId,
      parentSpanId: options.parentSpanId,
    });
  }

  async trace(name: string, options: TraceOptions): Promise<IngestResult> {
    return this.ingest({
      kind: "trace",
      name,
      level: options.level ?? "info",
      traceId: options.traceId,
      spanId: options.spanId,
      parentSpanId: options.parentSpanId,
      attrs: options.attrs,
      body: options.body,
      ts: options.ts,
      source: options.source,
    });
  }

  async metric(name: string, options: MetricOptions): Promise<IngestResult> {
    const attrs: JsonObject = { ...(options.attrs ?? {}), value: options.value };
    if (options.unit) {
      attrs["unit"] = options.unit;
    }
    return this.ingest({
      kind: "metric",
      name,
      attrs,
      body: options.body,
      ts: options.ts,
      source: options.source,
    });
  }

  async logs(params: QueryParams = {}): Promise<Record<string, unknown>> {
    return this.query("/api/logs", params);
  }

  async traces(params: QueryParams = {}): Promise<Record<string, unknown>> {
    return this.query("/api/traces", params);
  }

  async stats(params: QueryParams = {}): Promise<Record<string, unknown>> {
    return this.query("/api/stats", params);
  }

  protected async query(path: string, params: QueryParams): Promise<Record<string, unknown>> {
    const query: QueryParams = { ...params };
    if (query["project_id"] === undefined && query["projectId"] === undefined && this.projectId) {
      query["project_id"] = this.projectId;
    }
    return this.request("GET", `${path}${queryString(query)}`);
  }

  protected async request(
    method: string,
    path: string,
    options: { headers?: Record<string, string>; jsonBody?: unknown } = {},
  ): Promise<Record<string, unknown>> {
    const headers: Record<string, string> = { Accept: "application/json", ...(options.headers ?? {}) };
    let body: string | undefined;
    if (options.jsonBody !== undefined) {
      body = JSON.stringify(options.jsonBody);
      headers["Content-Type"] = "application/json";
    }

    const response = await this.transport({
      method,
      url: this.baseUrl + path,
      headers,
      body,
      timeoutMs: this.timeoutMs,
    });
    if (response.status < 200 || response.status >= 300) {
      throw new VigilHTTPError(response.status, errorMessage(response.status, response.body), response.body);
    }
    if (!response.body) {
      return {};
    }
    return objectFromUnknown(JSON.parse(response.body));
  }

  protected requireIngestConfig(): void {
    const missing: string[] = [];
    if (!this.projectId) {
      missing.push("projectId");
    }
    if (!this.ingestKey) {
      missing.push("ingestKey");
    }
    if (missing.length > 0) {
      throw new VigilConfigError(`missing required ingest configuration: ${missing.join(", ")}`);
    }
  }
}

export class NoopVigilClient extends VigilClient {
  override readonly disabled = true;
  override readonly disabledReason: string;

  constructor(options: VigilClientOptions & { disabledReason?: string | undefined } = {}) {
    super({ ...options, transport: noopTransport });
    this.disabledReason = options.disabledReason ?? "Vigil is disabled";
  }

  override async health(): Promise<Record<string, unknown>> {
    return { status: "disabled", app: "vigil", reason: this.disabledReason };
  }

  override async createProject(_name: string): Promise<ProjectResult> {
    return { project: {}, ingestKey: "" };
  }

  override async listProjects(): Promise<Record<string, unknown>[]> {
    return [];
  }

  override async rotateKey(_projectId?: string): Promise<ProjectResult> {
    return { project: {}, ingestKey: "" };
  }

  override async ingest(_options: IngestOptions): Promise<IngestResult> {
    return { eventId: "", receivedAt: "", indexedAsync: false };
  }

  override async logs(_params: QueryParams = {}): Promise<Record<string, unknown>> {
    return { events: [], page: 1, limit: 0, total: 0, sync: await this.health() };
  }

  override async traces(_params: QueryParams = {}): Promise<Record<string, unknown>> {
    return { traces: [], page: 1, limit: 0, total: 0, sync: await this.health() };
  }

  override async stats(_params: QueryParams = {}): Promise<Record<string, unknown>> {
    return {
      total_events: 0,
      by_kind: [],
      by_level: [],
      token_total: 0,
      cost_total: 0,
      volume: [],
      sync: await this.health(),
    };
  }
}

export class BrowserVigilClient {
  readonly baseUrl: string;
  readonly browserIngestKey: string;
  readonly source: string;
  readonly timeoutMs: number;
  readonly disabled: boolean = false;
  readonly disabledReason: string = "";

  protected readonly transport: Transport;

  constructor(options: BrowserVigilClientOptions = {}) {
    this.baseUrl = normalizeBaseUrl(options.baseUrl ?? browserDefaultBaseUrl());
    this.browserIngestKey = clean(options.browserIngestKey);
    this.source = clean(options.source) || "browser";
    this.timeoutMs = options.timeoutMs ?? 10_000;
    this.transport = options.transport ?? fetchTransport;
  }

  static fromEnv(options: BrowserFromEnvOptions = {}): BrowserVigilClient {
    const env = options.env ?? defaultEnv();
    const baseUrl = env.VIGIL_BASE_URL ?? browserDefaultBaseUrl();
    const browserIngestKey = env.VIGIL_BROWSER_INGEST_KEY;
    const source = options.source ?? env.VIGIL_SOURCE ?? "browser";

    if (!clean(browserIngestKey)) {
      if (options.optional) {
        return new NoopBrowserVigilClient({
          baseUrl,
          browserIngestKey,
          source,
          timeoutMs: options.timeoutMs,
          disabledReason: "missing required environment variable: VIGIL_BROWSER_INGEST_KEY",
        });
      }
      throw new VigilConfigError("missing required environment variable: VIGIL_BROWSER_INGEST_KEY");
    }

    return new BrowserVigilClient({
      baseUrl,
      browserIngestKey,
      source,
      timeoutMs: options.timeoutMs,
      transport: options.transport,
    });
  }

  async ingest(options: BrowserIngestOptions): Promise<IngestResult> {
    this.requireBrowserIngestConfig();
    const envelope: Record<string, JsonValue | undefined> = {
      schema_version: 1,
      kind: (options.kind ?? "log").toLowerCase(),
      ts: formatTimestamp(options.ts),
      source: clean(options.source) || this.source,
      name: required("name", options.name),
      attrs: options.attrs ?? {},
      body: options.body ?? null,
    };
    setIfPresent(envelope, "level", options.level);
    setIfPresent(envelope, "trace_id", options.traceId);
    setIfPresent(envelope, "span_id", options.spanId);
    setIfPresent(envelope, "parent_span_id", options.parentSpanId);

    const payload = await requestWithTransport(this.transport, {
      baseUrl: this.baseUrl,
      method: "POST",
      path: "/api/browser/ingest",
      timeoutMs: this.timeoutMs,
      headers: { Authorization: `Bearer ${this.browserIngestKey}` },
      jsonBody: envelope,
    });
    return ingestResultFromJson(payload);
  }

  async log(name: string, options: BrowserLogOptions = {}): Promise<IngestResult> {
    const body = options.body === undefined && options.message !== undefined
      ? { message: options.message }
      : options.body;
    return this.ingest({
      kind: "log",
      name,
      level: options.level ?? "info",
      attrs: options.attrs,
      body,
      ts: options.ts,
      source: options.source,
      traceId: options.traceId,
      spanId: options.spanId,
      parentSpanId: options.parentSpanId,
    });
  }

  protected requireBrowserIngestConfig(): void {
    if (!this.browserIngestKey) {
      throw new VigilConfigError("missing required browser ingest configuration: browserIngestKey");
    }
  }
}

export class NoopBrowserVigilClient extends BrowserVigilClient {
  override readonly disabled = true;
  override readonly disabledReason: string;

  constructor(options: BrowserVigilClientOptions & { disabledReason?: string | undefined } = {}) {
    super({ ...options, transport: noopTransport });
    this.disabledReason = options.disabledReason ?? "Vigil browser capture is disabled";
  }

  override async ingest(_options: BrowserIngestOptions): Promise<IngestResult> {
    return { eventId: "", receivedAt: "", indexedAsync: false };
  }
}

export function startVigilBrowserCapture(options: BrowserCaptureOptions = {}): BrowserCaptureHandle {
  const win = options.window ?? currentWindow();
  const client = options.client ?? new BrowserVigilClient(options);
  const cleanup: Array<() => void> = [];
  const captureConsoleErrors = options.captureConsoleErrors ?? true;
  const captureErrors = options.captureErrors ?? true;
  const captureUnhandledRejections = options.captureUnhandledRejections ?? true;
  const capturePageViews = options.capturePageViews ?? true;
  const captureRouteChanges = options.captureRouteChanges ?? true;
  const captureFetchSpans = options.captureFetchSpans ?? true;
  const captureFetchFailures = options.captureFetchFailures ?? true;
  const propagateTraceHeaders = options.propagateTraceHeaders ?? true;
  const baseAttrs = options.baseAttrs ?? {};

  if (!win) {
    return { client, stop: () => undefined };
  }

  const send = (name: string, attrs: JsonObject = {}, body: JsonValue = null, level = "error", trace?: TraceContext): void => {
    void client.log(name, {
      level,
      attrs: { ...browserContext(win), ...baseAttrs, ...attrs },
      body,
      traceId: trace?.traceId,
      spanId: trace?.spanId,
      parentSpanId: trace?.parentSpanId,
    }).catch(() => undefined);
  };

  if (capturePageViews) {
    send("browser.page_view", {}, null, "info");
  }

  if (captureErrors) {
    const onError = (event: unknown): void => {
      const errorEvent = objectFromUnknown(event);
      send("browser.error", {
        message: safeString(errorEvent["message"]),
        filename: pathOnly(stringFromUnknown(errorEvent["filename"])),
        line: numberFromUnknown(errorEvent["lineno"]),
        column: numberFromUnknown(errorEvent["colno"]),
      }, errorBody(errorEvent["error"], stringFromUnknown(errorEvent["message"]) || "Browser error"));
    };
    win.addEventListener("error", onError);
    cleanup.push(() => win.removeEventListener("error", onError));
  }

  if (captureUnhandledRejections) {
    const onUnhandledRejection = (event: unknown): void => {
      const rejectionEvent = objectFromUnknown(event);
      send("browser.unhandled_rejection", {}, errorBody(rejectionEvent["reason"], "Unhandled promise rejection"));
    };
    win.addEventListener("unhandledrejection", onUnhandledRejection);
    cleanup.push(() => win.removeEventListener("unhandledrejection", onUnhandledRejection));
  }

  if (captureConsoleErrors && win.console?.error) {
    const originalError = win.console.error.bind(win.console);
    win.console.error = (...args: unknown[]): void => {
      send("browser.console_error", {}, { args: args.slice(0, 5).map(summaryString) });
      originalError(...args);
    };
    cleanup.push(() => {
      if (win.console) {
        win.console.error = originalError;
      }
    });
  }

  if (captureRouteChanges) {
    installRouteCapture(win, send, cleanup);
  }

  if ((captureFetchSpans || captureFetchFailures || propagateTraceHeaders) && typeof win.fetch === "function") {
    installFetchCapture(win, send, cleanup, {
      captureFetchSpans,
      captureFetchFailures,
      propagateTraceHeaders,
      vigilBaseUrl: client.baseUrl,
    });
  }

  return {
    client,
    stop() {
      while (cleanup.length > 0) {
        cleanup.pop()?.();
      }
    },
  };
}

async function fetchTransport(request: TransportRequest): Promise<TransportResponse> {
  const fetchFn = globalThis.fetch;
  if (typeof fetchFn !== "function") {
    throw new VigilConfigError("global fetch is not available; use Node 18+, Bun, or pass a custom transport");
  }

  const controller = new AbortController();
  const timeout = globalThis.setTimeout(() => controller.abort(), request.timeoutMs);
  try {
    const init: RequestInit = {
      method: request.method,
      headers: request.headers,
      signal: controller.signal,
    };
    if (request.body !== undefined) {
      init.body = request.body;
    }
    const response = await fetchFn(request.url, init);
    return { status: response.status, body: await response.text() };
  } finally {
    globalThis.clearTimeout(timeout);
  }
}

function noopTransport(_request: TransportRequest): TransportResponse {
  return { status: 204, body: "{}" };
}

async function requestWithTransport(
  transport: Transport,
  options: {
    baseUrl: string;
    method: string;
    path: string;
    timeoutMs: number;
    headers?: Record<string, string> | undefined;
    jsonBody?: unknown;
  },
): Promise<Record<string, unknown>> {
  const headers: Record<string, string> = { Accept: "application/json", ...(options.headers ?? {}) };
  let body: string | undefined;
  if (options.jsonBody !== undefined) {
    body = JSON.stringify(options.jsonBody);
    headers["Content-Type"] = "application/json";
  }
  const response = await transport({
    method: options.method,
    url: options.baseUrl + options.path,
    headers,
    body,
    timeoutMs: options.timeoutMs,
  });
  if (response.status < 200 || response.status >= 300) {
    throw new VigilHTTPError(response.status, errorMessage(response.status, response.body), response.body);
  }
  if (!response.body) {
    return {};
  }
  return objectFromUnknown(JSON.parse(response.body));
}

function normalizeBaseUrl(raw: string): string {
  const value = required("baseUrl", raw).replace(/\/+$/, "");
  if (!value.startsWith("http://") && !value.startsWith("https://")) {
    throw new VigilConfigError("baseUrl must start with http:// or https://");
  }
  return value;
}

function formatTimestamp(value?: Date | string): string {
  if (value === undefined) {
    return new Date().toISOString();
  }
  if (typeof value === "string") {
    return value;
  }
  return value.toISOString();
}

function queryString(params: QueryParams): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null) {
      continue;
    }
    const normalizedKey = key === "projectId" ? "project_id" : key;
    query.set(normalizedKey, value instanceof Date ? value.toISOString() : String(value));
  }
  const encoded = query.toString();
  return encoded ? `?${encoded}` : "";
}

function ingestResultFromJson(payload: Record<string, unknown>): IngestResult {
  return {
    eventId: stringFromUnknown(payload["event_id"]),
    receivedAt: stringFromUnknown(payload["received_at"]),
    indexedAsync: Boolean(payload["indexed_async"]),
  };
}

function projectResultFromJson(payload: Record<string, unknown>): ProjectResult {
  return {
    project: objectFromUnknown(payload["project"]),
    ingestKey: stringFromUnknown(payload["ingest_key"]),
  };
}

function errorMessage(statusCode: number, body: string): string {
  try {
    const payload = objectFromUnknown(JSON.parse(body));
    const error = payload["error"];
    if (error) {
      return `Vigil returned ${statusCode}: ${String(error)}`;
    }
  } catch {
    // Ignore parse errors; fall through to a generic message.
  }
  return `Vigil returned HTTP ${statusCode}`;
}

function defaultEnv(): VigilEnv {
  const maybeProcess = globalThis as typeof globalThis & { process?: { env?: VigilEnv } };
  return maybeProcess.process?.env ?? {};
}

function currentWindow(): BrowserWindowLike | undefined {
  return typeof window === "undefined" ? undefined : window;
}

function browserDefaultBaseUrl(): string {
  const win = currentWindow();
  return win?.location?.origin ?? "http://localhost:8080";
}

function required(name: string, value: unknown): string {
  const cleaned = clean(value);
  if (!cleaned) {
    throw new VigilConfigError(`${name} is required`);
  }
  return cleaned;
}

function clean(value: unknown): string {
  return value === undefined || value === null ? "" : String(value).trim();
}

function setIfPresent(target: Record<string, JsonValue | undefined>, key: string, value: unknown): void {
  const cleaned = clean(value);
  if (cleaned) {
    target[key] = cleaned;
  }
}

function objectFromUnknown(value: unknown): Record<string, unknown> {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    return { ...(value as Record<string, unknown>) };
  }
  return {};
}

function stringFromUnknown(value: unknown): string {
  return value === undefined || value === null ? "" : String(value);
}

function numberFromUnknown(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function browserContext(win: BrowserWindowLike): JsonObject {
  return {
    path: win.location?.pathname ?? "",
    referrer: safeReferrer(win.document?.referrer),
    language: win.navigator?.language,
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    viewport_width: win.innerWidth,
    viewport_height: win.innerHeight,
    user_agent: win.navigator?.userAgent,
  };
}

function safeReferrer(raw: string | undefined): string {
  if (!raw) {
    return "";
  }
  return pathOnly(raw);
}

function pathOnly(raw: string | undefined): string {
  if (!raw) {
    return "";
  }
  try {
    const parsed = new URL(raw, currentWindow()?.location?.origin ?? "http://localhost");
    return parsed.pathname;
  } catch {
    return "";
  }
}

function errorBody(error: unknown, fallbackMessage: string): JsonObject {
  if (error instanceof Error) {
    return {
      message: safeString(error.message || fallbackMessage),
      name: safeString(error.name),
      stack: safeString(error.stack),
    };
  }
  return { message: safeString(fallbackMessage), value: summaryString(error) };
}

function summaryString(value: unknown): string {
  if (value instanceof Error) {
    return `${value.name}: ${value.message}`.slice(0, 1_000);
  }
  if (typeof value === "string") {
    return value.slice(0, 1_000);
  }
  try {
    return JSON.stringify(value)?.slice(0, 1_000) ?? String(value);
  } catch {
    return String(value).slice(0, 1_000);
  }
}

function safeString(value: unknown): string {
  return value === undefined || value === null ? "" : String(value).slice(0, 2_000);
}

function installRouteCapture(
  win: BrowserWindowLike,
  send: (name: string, attrs?: JsonObject, body?: JsonValue, level?: string) => void,
  cleanup: Array<() => void>,
): void {
  const history = win.history;
  if (!history) {
    return;
  }

  let lastPath = win.location?.pathname ?? "";
  const captureRoute = (): void => {
    const nextPath = win.location?.pathname ?? "";
    if (nextPath === lastPath) {
      return;
    }
    const previousPath = lastPath;
    lastPath = nextPath;
    send("browser.route_change", { previous_path: previousPath, path: nextPath }, null, "info");
  };

  const originalPushState = history.pushState.bind(history);
  const originalReplaceState = history.replaceState.bind(history);
  history.pushState = (...args): void => {
    originalPushState(...args);
    captureRoute();
  };
  history.replaceState = (...args): void => {
    originalReplaceState(...args);
    captureRoute();
  };
  win.addEventListener("popstate", captureRoute);
  cleanup.push(() => {
    history.pushState = originalPushState;
    history.replaceState = originalReplaceState;
    win.removeEventListener("popstate", captureRoute);
  });
}

function installFetchCapture(
  win: BrowserWindowLike,
  send: (name: string, attrs?: JsonObject, body?: JsonValue, level?: string, trace?: TraceContext) => void,
  cleanup: Array<() => void>,
  options: {
    captureFetchSpans: boolean;
    captureFetchFailures: boolean;
    propagateTraceHeaders: boolean;
    vigilBaseUrl: string;
  },
): void {
  if (!win.fetch) {
    return;
  }
  const originalFetch = win.fetch.bind(win);
  win.fetch = async (input: unknown, init?: { method?: string | undefined }): Promise<{ ok: boolean; status: number }> => {
    const started = Date.now();
    const method = fetchMethod(input, init);
    const path = fetchPath(input);
    const url = fetchURL(input);
    const skip = isVigilIngestURL(url, options.vigilBaseUrl);
    const trace = !skip && (options.propagateTraceHeaders || options.captureFetchSpans)
      ? createTraceContext()
      : undefined;
    const [nextInput, nextInit] = trace && options.propagateTraceHeaders
      ? withTraceparent(input, init, trace)
      : [input, init];
    try {
      const response = await originalFetch(nextInput, nextInit);
      if (!skip && options.captureFetchSpans) {
        send("browser.fetch", {
          method,
          path,
          status: response.status,
          duration_ms: Date.now() - started,
        }, null, response.ok ? "info" : "error", trace);
      }
      if (!skip && options.captureFetchFailures && !response.ok) {
        send("browser.fetch_failed", {
          method,
          path,
          status: response.status,
          duration_ms: Date.now() - started,
        }, null, "error", trace);
      }
      return response;
    } catch (error) {
      if (!skip && options.captureFetchFailures) {
        send("browser.fetch_error", {
          method,
          path,
          duration_ms: Date.now() - started,
        }, errorBody(error, "fetch failed"), "error", trace);
      }
      if (!skip && options.captureFetchSpans) {
        send("browser.fetch", {
          method,
          path,
          duration_ms: Date.now() - started,
          error: true,
        }, null, "error", trace);
      }
      throw error;
    }
  };
  cleanup.push(() => {
    win.fetch = originalFetch;
  });
}

function withTraceparent(
  input: unknown,
  init: ({ method?: string | undefined; headers?: HeadersInit | undefined } | undefined),
  trace: TraceContext,
): [unknown, ({ method?: string | undefined; headers?: HeadersInit | undefined } | undefined)] {
  const header = formatTraceparent(trace);
  const nextInit = { ...(init ?? {}) };
  nextInit.headers = mergeHeader(nextInit.headers ?? requestHeaders(input), "traceparent", header);
  return [input, nextInit];
}

function mergeHeader(headers: HeadersInit | undefined, name: string, value: string): HeadersInit {
  if (typeof Headers !== "undefined") {
    const next = new Headers(headers);
    next.set(name, value);
    return next;
  }
  if (Array.isArray(headers)) {
    const filtered = headers.filter(([key]) => key.toLowerCase() !== name.toLowerCase());
    return [...filtered, [name, value]];
  }
  return { ...(headers ?? {}), [name]: value };
}

function requestHeaders(input: unknown): HeadersInit | undefined {
  if (typeof Request !== "undefined" && input instanceof Request) {
    return input.headers;
  }
  return undefined;
}

function fetchURL(input: unknown): string {
  if (typeof input === "string") {
    return input;
  }
  if (input instanceof URL) {
    return input.toString();
  }
  if (typeof Request !== "undefined" && input instanceof Request) {
    return input.url;
  }
  return "";
}

function isVigilIngestURL(rawURL: string, baseUrl: string): boolean {
  if (!rawURL) {
    return false;
  }
  try {
    const base = new URL(baseUrl);
    const target = new URL(rawURL, currentWindow()?.location?.origin ?? base.origin);
    return target.origin === base.origin
      && (target.pathname === "/api/ingest" || target.pathname === "/api/browser/ingest");
  } catch {
    return false;
  }
}

function randomHex(bytes: number): string {
  const values = new Uint8Array(bytes);
  const cryptoLike = globalThis.crypto;
  if (cryptoLike?.getRandomValues) {
    cryptoLike.getRandomValues(values);
  } else {
    for (let i = 0; i < values.length; i += 1) {
      values[i] = Math.floor(Math.random() * 256);
    }
  }
  if (values.every((value) => value === 0)) {
    values[values.length - 1] = 1;
  }
  return [...values].map((value) => value.toString(16).padStart(2, "0")).join("");
}

function isValidTraceId(value: string): boolean {
  return /^[0-9a-f]{32}$/.test(value) && !/^0+$/.test(value);
}

function isValidSpanId(value: string): boolean {
  return /^[0-9a-f]{16}$/.test(value) && !/^0+$/.test(value);
}

function fetchMethod(input: unknown, init?: { method?: string | undefined }): string {
  if (init?.method) {
    return init.method.toUpperCase();
  }
  if (typeof Request !== "undefined" && input instanceof Request) {
    return input.method.toUpperCase();
  }
  return "GET";
}

function fetchPath(input: unknown): string {
  if (typeof input === "string") {
    return pathOnly(input);
  }
  if (input instanceof URL) {
    return input.pathname;
  }
  if (typeof Request !== "undefined" && input instanceof Request) {
    return pathOnly(input.url);
  }
  return "";
}
