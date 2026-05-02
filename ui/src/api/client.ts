export type Project = {
  id: string;
  name: string;
  status: string;
  created_at: string;
  updated_at: string;
};

export type SyncStatus = {
  latest_ingested_at: string;
  latest_indexed_at: string;
  last_rebuild_at: string;
  last_error?: string;
  stale: boolean;
};

export type ResultWarning = {
  code: string;
  message: string;
};

export type RetentionStatus = {
  enabled: boolean;
  dry_run: boolean;
  days: number;
  sweep_interval: string;
  last_run_at?: string;
  last_success_at?: string;
  last_error?: string;
  last_cutoff_day?: string;
  last_deleted_day_dirs: number;
  last_deleted_files: number;
  last_deleted_bytes: number;
};

export type EventRecord = {
  event_id: string;
  received_at: string;
  schema_version: number;
  project_id: string;
  kind: "log" | "trace" | "metric";
  ts: string;
  source: string;
  trace_id?: string;
  span_id?: string;
  level?: string;
  name: string;
  attrs: Record<string, unknown>;
  body: unknown;
};

export type LogResponse = {
  events: EventRecord[];
  page: number;
  limit: number;
  total: number;
  warnings?: ResultWarning[];
  sync: SyncStatus;
};

export type TraceEvent = {
  event_id: string;
  ts: string;
  name: string;
  level?: string;
  source: string;
  span_id?: string;
  attrs: Record<string, unknown>;
  body: unknown;
};

export type TraceTimeline = {
  trace_id: string;
  project_id: string;
  name: string;
  started_at: string;
  ended_at: string;
  event_count: number;
  events: TraceEvent[];
};

export type TraceResponse = {
  traces: TraceTimeline[];
  page: number;
  limit: number;
  total: number;
  warnings?: ResultWarning[];
  sync: SyncStatus;
};

export type StatsSummary = {
  total_events: number;
  by_kind: Array<{ label: string; count: number }>;
  by_level: Array<{ label: string; count: number }>;
  token_total: number;
  cost_total: number;
  volume: Array<{ day: string; count: number }>;
  sync: SyncStatus;
};

type ProjectCreateResponse = {
  project: Project;
  ingest_key: string;
};

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {})
    },
    ...init
  });

  const contentType = response.headers.get("content-type") ?? "";
  const payload = contentType.includes("application/json")
    ? await response.json()
    : await response.text();

  if (!response.ok) {
    const message =
      typeof payload === "object" &&
      payload !== null &&
      "error" in payload &&
      typeof payload.error === "string"
        ? payload.error
        : `Request failed with status ${response.status}`;
    throw new Error(message);
  }

  return payload as T;
}

export function listProjects() {
  return request<{ projects: Project[] }>("/api/projects");
}

export function createProject(name: string) {
  return request<ProjectCreateResponse>("/api/projects", {
    method: "POST",
    body: JSON.stringify({ name })
  });
}

export function regenerateProjectKey(projectId: string) {
  return request<ProjectCreateResponse>(`/api/projects/${projectId}/keys/regenerate`, {
    method: "POST"
  });
}

export function getLogs(params: Record<string, string>) {
  const query = new URLSearchParams(params);
  return request<LogResponse>(`/api/logs?${query.toString()}`);
}

export function getTraces(params: Record<string, string>) {
  const query = new URLSearchParams(params);
  return request<TraceResponse>(`/api/traces?${query.toString()}`);
}

export function getStats(params: Record<string, string>) {
  const query = new URLSearchParams(params);
  return request<StatsSummary>(`/api/stats?${query.toString()}`);
}

export function getHealth() {
  return request<{ status: string; app: string; sync: SyncStatus; retention: RetentionStatus }>("/api/health");
}
