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
}

export interface LogOptions {
  message?: string | undefined;
  level?: string | undefined;
  attrs?: JsonObject | undefined;
  body?: JsonValue | undefined;
  ts?: Date | string | undefined;
  source?: string | undefined;
}

export interface TraceOptions {
  traceId: string;
  spanId?: string | undefined;
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
    });
  }

  async trace(name: string, options: TraceOptions): Promise<IngestResult> {
    return this.ingest({
      kind: "trace",
      name,
      level: options.level ?? "info",
      traceId: options.traceId,
      spanId: options.spanId,
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
