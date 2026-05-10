import { useEffect, useMemo, useRef, useState } from "react";
import {
  createProject,
  EventRecord,
  getHealth,
  getLogs,
  getStats,
  getTraces,
  listProjects,
  Project,
  regenerateProjectKey,
  ResultWarning,
  StatsSummary,
  TraceResponse
} from "./api/client";
import { usePolling } from "./hooks/usePolling";

type Tab = "logs" | "traces" | "stats";
type QuickRange = "5m" | "15m" | "1h" | "6h" | "24h" | "custom";
type ThemeMode = "light" | "dark";

type KeyState = {
  project: Project;
  ingestKey: string;
};

type RouteState =
  | { page: "projects" }
  | { page: "project"; projectId: string; tab: Tab };

const defaultLogFilters = {
  q: "",
  level: "",
  kind: "",
  name: "",
  page: "1",
  limit: "50"
};

const quickRangeOptions: Array<{ value: Exclude<QuickRange, "custom">; label: string }> = [
  { value: "5m", label: "5m" },
  { value: "15m", label: "15m" },
  { value: "1h", label: "1h" },
  { value: "6h", label: "6h" },
  { value: "24h", label: "24h" }
];

const limitOptions = ["25", "50", "100"];

type LogFilterState = typeof defaultLogFilters & {
  from: string;
  to: string;
};

type ExplorerUrlState = {
  filters: LogFilterState;
  timePreset: QuickRange;
  liveTail: boolean;
};

function getInitialTheme(): ThemeMode {
  const stored = window.localStorage.getItem("vigil-theme");
  if (stored === "light" || stored === "dark") {
    return stored;
  }
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function getInitialDrawerState() {
  return false;
}

function quickRangeMs(range: Exclude<QuickRange, "custom">) {
  switch (range) {
    case "15m":
      return 15 * 60 * 1000;
    case "1h":
      return 60 * 60 * 1000;
    case "6h":
      return 6 * 60 * 60 * 1000;
    case "24h":
      return 24 * 60 * 60 * 1000;
    case "5m":
    default:
      return 5 * 60 * 1000;
  }
}

function createRecentLogFilters(base = new Date()) {
  const to = base.toISOString();
  const from = new Date(base.getTime() - 5 * 60 * 1000).toISOString();
  return {
    ...defaultLogFilters,
    from,
    to
  };
}

function createFiltersForRange(
  range: Exclude<QuickRange, "custom">,
  current: LogFilterState = createRecentLogFilters(),
  base = new Date()
) {
  return {
    ...createRecentLogFilters(base),
    from: new Date(base.getTime() - quickRangeMs(range)).toISOString(),
    to: base.toISOString(),
    q: current.q,
    level: current.level,
    kind: current.kind,
    name: current.name,
    limit: current.limit,
    page: "1"
  };
}

function createFiltersAroundTimestamp(
  timestamp: string,
  current: LogFilterState = createRecentLogFilters(),
  windowMs = 60 * 60 * 1000
) {
  const anchor = new Date(timestamp);
  if (Number.isNaN(anchor.getTime())) {
    return createFiltersForRange("5m", current);
  }

  const to = new Date(anchor.getTime() + 60 * 1000);
  return {
    ...createRecentLogFilters(to),
    from: new Date(anchor.getTime() - windowMs).toISOString(),
    to: to.toISOString(),
    q: current.q,
    level: current.level,
    kind: current.kind,
    name: current.name,
    limit: current.limit,
    page: "1"
  };
}

function toLocalDateTimeValue(iso: string) {
  const value = new Date(iso);
  if (Number.isNaN(value.getTime())) {
    return "";
  }

  const offset = value.getTimezoneOffset() * 60 * 1000;
  return new Date(value.getTime() - offset).toISOString().slice(0, 16);
}

function fromLocalDateTimeValue(value: string) {
  if (!value) {
    return "";
  }

  const next = new Date(value);
  if (Number.isNaN(next.getTime())) {
    return "";
  }

  return next.toISOString();
}

function resolveLiveRangePreset(preset: QuickRange): Exclude<QuickRange, "custom"> {
  return preset === "custom" ? "5m" : preset;
}

function isQuickRange(value: string | null): value is Exclude<QuickRange, "custom"> {
  return value === "5m" || value === "15m" || value === "1h" || value === "6h" || value === "24h";
}

function readExplorerUrlState(): ExplorerUrlState {
  const params = new URLSearchParams(window.location.search);
  const filters = createFiltersForRange("5m");
  const from = params.get("from");
  const to = params.get("to");

  if (from) filters.from = from;
  if (to) filters.to = to;

  const q = params.get("q");
  const kind = params.get("kind");
  const level = params.get("level");
  const name = params.get("name");
  const page = params.get("page");
  const limit = params.get("limit");

  if (q) filters.q = q;
  if (kind) filters.kind = kind;
  if (level) filters.level = level;
  if (name) filters.name = name;
  if (page) filters.page = page;
  if (limit) filters.limit = limit;

  const range = params.get("range");
  const timePreset: QuickRange = isQuickRange(range) ? range : from || to ? "custom" : "5m";

  return {
    filters,
    timePreset,
    liveTail: params.get("live") === "1"
  };
}

function buildExplorerUrl(route: RouteState, state: ExplorerUrlState) {
  const url = new URL(window.location.href);
  if (route.page === "projects") {
    url.pathname = "/";
    url.search = "";
    return url.toString();
  }

  url.pathname = `/projects/${route.projectId}/${route.tab}`;
  const params = new URLSearchParams();
  params.set("range", state.timePreset);
  params.set("from", state.filters.from);
  params.set("to", state.filters.to);
  if (state.filters.q) params.set("q", state.filters.q);
  if (state.filters.kind) params.set("kind", state.filters.kind);
  if (state.filters.level) params.set("level", state.filters.level);
  if (state.filters.name) params.set("name", state.filters.name);
  if (state.filters.page !== "1") params.set("page", state.filters.page);
  if (state.filters.limit !== defaultLogFilters.limit) params.set("limit", state.filters.limit);
  if (state.liveTail) params.set("live", "1");
  url.search = params.toString();
  return url.toString();
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  return value as Record<string, unknown>;
}

function formatInlineValue(value: unknown) {
  if (value === null || value === undefined) return "null";
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  return JSON.stringify(value);
}

function eventInlineFields(event: EventRecord, limit = 4) {
  const values: string[] = [];
  const attrs = asRecord(event.attrs) ?? {};
  const body = asRecord(event.body) ?? {};

  for (const [key, value] of Object.entries(attrs)) {
    values.push(`${key}=${formatInlineValue(value)}`);
    if (values.length >= limit) return values;
  }

  for (const [key, value] of Object.entries(body)) {
    if (key === "message") continue;
    values.push(`${key}=${formatInlineValue(value)}`);
    if (values.length >= limit) return values;
  }

  return values;
}

function formatDateTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value || "n/a";
  }
  return date.toLocaleString();
}

function formatTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value || "n/a";
  }
  return date.toLocaleTimeString();
}

function shortId(value?: string, length = 10) {
  if (!value) return "n/a";
  if (value.length <= length) return value;
  return value.slice(0, length);
}

function formatRangeLabel(filters: LogFilterState, preset: QuickRange) {
  if (preset === "custom") {
    return `${formatDateTime(filters.from)} to ${formatDateTime(filters.to)}`;
  }
  return `Last ${preset}`;
}

function timestampFallsInsideRange(timestamp: string, filters: LogFilterState) {
  const value = new Date(timestamp).getTime();
  const from = new Date(filters.from).getTime();
  const to = new Date(filters.to).getTime();
  if (Number.isNaN(value) || Number.isNaN(from) || Number.isNaN(to)) {
    return true;
  }
  return value >= from && value <= to;
}

function downloadBlob(filename: string, content: string, type: string) {
  const blob = new Blob([content], { type });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  URL.revokeObjectURL(url);
}

async function copyToClipboard(text: string) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }

  const input = document.createElement("textarea");
  input.value = text;
  document.body.appendChild(input);
  input.select();
  document.execCommand("copy");
  document.body.removeChild(input);
}

function readRoute(): RouteState {
  const parts = window.location.pathname.split("/").filter(Boolean);
  if (parts.length >= 2 && parts[0] === "projects") {
    const tab = parts[2];
    if (tab === "logs" || tab === "traces" || tab === "stats" || tab === undefined) {
      return { page: "project", projectId: parts[1], tab: (tab ?? "logs") as Tab };
    }
  }
  return { page: "projects" };
}

function navigate(next: RouteState) {
  if (next.page === "projects") {
    window.history.pushState({}, "", "/");
    return;
  }

  window.history.pushState({}, "", `/projects/${next.projectId}/${next.tab}`);
}

function eventMessage(event: EventRecord): string {
  if (event.body && typeof event.body === "object" && event.body !== null && "message" in event.body) {
    const message = (event.body as Record<string, unknown>).message;
    if (typeof message === "string" && message.trim()) {
      return message;
    }
  }

  if (event.attrs && typeof event.attrs === "object" && event.attrs !== null && "message" in event.attrs) {
    const message = (event.attrs as Record<string, unknown>).message;
    if (typeof message === "string" && message.trim()) {
      return message;
    }
  }

  return event.name;
}

function themeLabel(theme: ThemeMode) {
  return theme === "dark" ? "Dark" : "Light";
}

export default function App() {
  const initialExplorerState = readExplorerUrlState();
  const [theme, setTheme] = useState<ThemeMode>(() => getInitialTheme());
  const [filterDrawerOpen, setFilterDrawerOpen] = useState(() => getInitialDrawerState());
  const [route, setRoute] = useState<RouteState>(() => readRoute());
  const [projects, setProjects] = useState<Project[]>([]);
  const [createName, setCreateName] = useState("");
  const [latestKey, setLatestKey] = useState<KeyState | null>(null);
  const [searchText, setSearchText] = useState(initialExplorerState.filters.q);
  const [logFilters, setLogFilters] = useState<LogFilterState>(initialExplorerState.filters);
  const [timePreset, setTimePreset] = useState<QuickRange>(initialExplorerState.timePreset);
  const [liveTail, setLiveTail] = useState(initialExplorerState.liveTail);
  const [logs, setLogs] = useState<Awaited<ReturnType<typeof getLogs>> | null>(null);
  const [traces, setTraces] = useState<TraceResponse | null>(null);
  const [stats, setStats] = useState<StatsSummary | null>(null);
  const [health, setHealth] = useState<Awaited<ReturnType<typeof getHealth>> | null>(null);
  const [selectedEvent, setSelectedEvent] = useState<EventRecord | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [healthError, setHealthError] = useState<string | null>(null);
  const [loading, setLoading] = useState({
    projects: false,
    logs: false,
    traces: false,
    stats: false
  });

  const currentProjectId = route.page === "project" ? route.projectId : "";
  const currentTab = route.page === "project" ? route.tab : "logs";
  const currentProject = useMemo(
    () => projects.find((project) => project.id === currentProjectId) ?? null,
    [projects, currentProjectId]
  );
  const liveRangePreset = resolveLiveRangePreset(timePreset);

  const syncStatus = useMemo(() => {
    if (currentTab === "logs") return logs?.sync ?? null;
    if (currentTab === "traces") return traces?.sync ?? null;
    return stats?.sync ?? null;
  }, [currentTab, logs?.sync, traces?.sync, stats?.sync]);
  const latestIndexedAt = syncStatus?.latest_indexed_at || syncStatus?.latest_ingested_at || "";

  const currentWarnings = useMemo<ResultWarning[]>(() => {
    if (currentTab === "logs") return logs?.warnings ?? [];
    if (currentTab === "traces") return traces?.warnings ?? [];
    return [];
  }, [currentTab, logs?.warnings, traces?.warnings]);

  const currentTotal = useMemo(() => {
    if (currentTab === "logs") return logs?.total ?? 0;
    if (currentTab === "traces") return traces?.total ?? 0;
    return stats?.total_events ?? 0;
  }, [currentTab, logs?.total, traces?.total, stats?.total_events]);

  const latestDataOutsideWindow =
    route.page === "project" &&
    !liveTail &&
    currentTotal === 0 &&
    latestIndexedAt !== "" &&
    !timestampFallsInsideRange(latestIndexedAt, logFilters);

  const retentionStatus = health?.retention ?? null;
  const logFiltersRef = useRef(logFilters);

  useEffect(() => {
    logFiltersRef.current = logFilters;
  }, [logFilters]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
    window.localStorage.setItem("vigil-theme", theme);
  }, [theme]);

  useEffect(() => {
    const onPopState = () => {
      const nextRoute = readRoute();
      const nextState = readExplorerUrlState();
      setRoute(nextRoute);
      setSearchText(nextState.filters.q);
      setLogFilters(nextState.filters);
      setTimePreset(nextState.timePreset);
      setLiveTail(nextState.liveTail);
      setSelectedEvent(null);
    };

    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  useEffect(() => {
    const timeout = window.setTimeout(() => setNotice(null), 2600);
    if (!notice) {
      window.clearTimeout(timeout);
    }
    return () => window.clearTimeout(timeout);
  }, [notice]);

  useEffect(() => {
    const nextUrl = buildExplorerUrl(route, {
      filters: logFilters,
      timePreset,
      liveTail
    });
    if (nextUrl !== window.location.href) {
      window.history.replaceState({}, "", nextUrl);
    }
  }, [route, logFilters, timePreset, liveTail]);

  async function loadHealth() {
    try {
      const response = await getHealth();
      setHealth(response);
      setHealthError(null);
    } catch (loadError) {
      setHealthError(loadError instanceof Error ? loadError.message : "Health check failed");
    }
  }

  async function loadProjects() {
    setLoading((current) => ({ ...current, projects: true }));
    try {
      const response = await listProjects();
      setProjects(response.projects);

      const nextRoute = readRoute();
      if (nextRoute.page === "project") {
        const exists = response.projects.some((project) => project.id === nextRoute.projectId);
        if (!exists) {
          setRoute({ page: "projects" });
          navigate({ page: "projects" });
        }
      }

      setError(null);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Failed to load projects");
    } finally {
      setLoading((current) => ({ ...current, projects: false }));
    }
  }

  async function loadLogs(projectId = currentProjectId, filters = logFilters, options?: { followLatest?: boolean }) {
    if (!projectId) return;
    setLoading((current) => ({ ...current, logs: true }));
    try {
      const response = await getLogs({
        ...filters,
        project_id: projectId
      });
      setLogs(response);
      if (
        options?.followLatest ||
        !selectedEvent ||
        !response.events.some((event) => event.event_id === selectedEvent.event_id)
      ) {
        setSelectedEvent(response.events[0] ?? null);
      }
      setError(null);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Failed to load logs");
    } finally {
      setLoading((current) => ({ ...current, logs: false }));
    }
  }

  async function loadTraces(projectId = currentProjectId, filters = logFilters) {
    if (!projectId) return;
    setLoading((current) => ({ ...current, traces: true }));
    try {
      const response = await getTraces({
        project_id: projectId,
        from: filters.from,
        to: filters.to,
        page: filters.page,
        limit: filters.limit
      });
      setTraces(response);
      setError(null);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Failed to load traces");
    } finally {
      setLoading((current) => ({ ...current, traces: false }));
    }
  }

  async function loadStats(projectId = currentProjectId, filters = logFilters) {
    if (!projectId) return;
    setLoading((current) => ({ ...current, stats: true }));
    try {
      const response = await getStats({
        project_id: projectId,
        from: filters.from,
        to: filters.to,
        page: "1",
        limit: filters.limit
      });
      setStats(response);
      setError(null);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "Failed to load stats");
    } finally {
      setLoading((current) => ({ ...current, stats: false }));
    }
  }

  function loadProjectTab(tab: Tab, projectId: string, filters: LogFilterState, options?: { followLatest?: boolean }) {
    if (tab === "logs") {
      return loadLogs(projectId, filters, options);
    }
    if (tab === "traces") {
      return loadTraces(projectId, filters);
    }
    return loadStats(projectId, filters);
  }

  useEffect(() => {
    void loadProjects();
    void loadHealth();
  }, []);

  usePolling(() => loadHealth(), true, 15000);

  useEffect(() => {
    if (route.page !== "project" || !route.projectId) {
      return;
    }

    const { projectId, tab } = route;
    const nextFilters = liveTail
      ? createFiltersForRange(liveRangePreset, logFilters)
      : { ...logFilters, page: "1" };

    if (liveTail || tab === "logs") {
      setLogFilters(nextFilters);
    }

    if (tab === "logs") {
      void loadLogs(projectId, nextFilters, { followLatest: liveTail });
      return;
    }
    if (tab === "traces") {
      void loadTraces(projectId, nextFilters);
      return;
    }
    void loadStats(projectId, nextFilters);
  }, [currentProjectId, currentTab, liveTail]);

  useEffect(() => {
    if (route.page !== "project" || route.tab !== "logs") {
      return;
    }

    const { projectId } = route;
    if (logFiltersRef.current.q === searchText) {
      return;
    }

    const timeout = window.setTimeout(() => {
      const nextFilters = {
        ...logFiltersRef.current,
        q: searchText,
        page: "1"
      };
      setLogFilters(nextFilters);
      void loadLogs(projectId, nextFilters);
    }, 300);

    return () => window.clearTimeout(timeout);
  }, [searchText, route.page, currentProjectId, currentTab]);

  usePolling(() => {
    if (route.page !== "project") {
      return;
    }

    const nextFilters = liveTail
      ? {
          ...createFiltersForRange(liveRangePreset, logFilters),
          page: "1"
        }
      : logFilters;

    if (liveTail) {
      setLogFilters(nextFilters);
    }

    if (route.tab === "logs") {
      return loadLogs(route.projectId, nextFilters, { followLatest: true });
    }
    if (route.tab === "traces") {
      return loadTraces(route.projectId, nextFilters);
    }
    return loadStats(route.projectId, nextFilters);
  }, route.page === "project" && liveTail, 2500);

  async function handleCreateProject() {
    if (!createName.trim()) {
      setError("Project name is required");
      return;
    }

    try {
      const result = await createProject(createName.trim());
      setLatestKey({ project: result.project, ingestKey: result.ingest_key });
      setCreateName("");
      await loadProjects();
      const nextRoute: RouteState = { page: "project", projectId: result.project.id, tab: "logs" };
      setRoute(nextRoute);
      navigate(nextRoute);
      setTimePreset("5m");
      setSearchText("");
      setLogFilters(createFiltersForRange("5m"));
      setLiveTail(false);
      setSelectedEvent(null);
      setError(null);
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "Failed to create project");
    }
  }

  async function handleRegenerate(projectId: string) {
    try {
      const result = await regenerateProjectKey(projectId);
      setLatestKey({ project: result.project, ingestKey: result.ingest_key });
      await loadProjects();
      setNotice(`New ingest key created for ${result.project.name}.`);
      setError(null);
    } catch (regenerateError) {
      setError(regenerateError instanceof Error ? regenerateError.message : "Failed to regenerate key");
    }
  }

  function openProject(projectId: string, tab: Tab = "logs") {
    const nextRoute: RouteState = { page: "project", projectId, tab };
    setRoute(nextRoute);
    navigate(nextRoute);
    setTimePreset("5m");
    setLogFilters(createFiltersForRange("5m"));
    setLiveTail(false);
    setFilterDrawerOpen(false);
    setSelectedEvent(null);
  }

  function changeTab(tab: Tab) {
    if (route.page !== "project") return;
    const nextRoute: RouteState = { page: "project", projectId: route.projectId, tab };
    setRoute(nextRoute);
    navigate(nextRoute);
    setLogFilters((current) => ({ ...current, page: "1" }));
  }

  function handleToggleLiveTail() {
    const nextLiveTail = !liveTail;
    setLiveTail(nextLiveTail);
    if (!nextLiveTail || route.page !== "project") {
      return;
    }

    const nextPreset = liveRangePreset;
    if (timePreset === "custom") {
      setTimePreset("5m");
    }
    const nextFilters = createFiltersForRange(nextPreset, logFilters);
    setLogFilters(nextFilters);

    if (route.tab === "logs") {
      void loadLogs(route.projectId, nextFilters, { followLatest: true });
      return;
    }
    if (route.tab === "traces") {
      void loadTraces(route.projectId, nextFilters);
      return;
    }
    void loadStats(route.projectId, nextFilters);
  }

  function handleRangePresetChange(range: Exclude<QuickRange, "custom">) {
    const nextFilters = createFiltersForRange(range, { ...logFilters, q: searchText });
    setTimePreset(range);
    setLiveTail(false);
    setLogFilters(nextFilters);
    if (route.page === "project") {
      void loadProjectTab(route.tab, route.projectId, nextFilters);
    }
  }

  function handleCustomTimeChange(field: "from" | "to", value: string) {
    const nextValue = fromLocalDateTimeValue(value);
    setTimePreset("custom");
    setLiveTail(false);
    setLogFilters((current) => ({
      ...current,
      [field]: nextValue,
      page: "1"
    }));
  }

  function handleFilterChange(field: keyof typeof defaultLogFilters, value: string) {
    setLogFilters((current) => ({
      ...current,
      [field]: value,
      page: "1"
    }));
  }

  function handleApplyFilters() {
    if (route.page !== "project") {
      return;
    }

    const nextFilters = { ...logFilters, q: searchText, page: "1" };
    setLogFilters(nextFilters);
    setFilterDrawerOpen(false);
    if (route.tab === "logs") {
      void loadLogs(route.projectId, nextFilters);
      return;
    }
    if (route.tab === "traces") {
      void loadTraces(route.projectId, nextFilters);
      return;
    }
    void loadStats(route.projectId, nextFilters);
  }

  function handleResetFilters() {
    const nextFilters = createFiltersForRange("5m");
    setTimePreset("5m");
    setLiveTail(false);
    setSearchText("");
    setLogFilters(nextFilters);
    setFilterDrawerOpen(false);
    if (route.page === "project") {
      if (route.tab === "logs") {
        void loadLogs(route.projectId, nextFilters);
        return;
      }
      if (route.tab === "traces") {
        void loadTraces(route.projectId, nextFilters);
        return;
      }
      void loadStats(route.projectId, nextFilters);
    }
  }

  function handleShowLatestIndexedData() {
    if (route.page !== "project" || !latestIndexedAt) {
      return;
    }

    const nextFilters = createFiltersAroundTimestamp(
      latestIndexedAt,
      { ...logFilters, q: searchText },
      Math.max(quickRangeMs(liveRangePreset), 60 * 60 * 1000)
    );
    setTimePreset("custom");
    setLiveTail(false);
    setSearchText(nextFilters.q);
    setLogFilters(nextFilters);
    setFilterDrawerOpen(false);
    void loadProjectTab(route.tab, route.projectId, nextFilters);
  }

  async function handleCopyShareUrl() {
    try {
      const shareUrl = buildExplorerUrl(route, {
        filters: logFilters,
        timePreset,
        liveTail
      });
      await copyToClipboard(shareUrl);
      setNotice("Copied filtered explorer URL.");
      setError(null);
    } catch (copyError) {
      setError(copyError instanceof Error ? copyError.message : "Failed to copy share URL");
    }
  }

  async function handleExportCurrentView() {
    if (route.page !== "project") {
      return;
    }

    try {
      const timestamp = new Date().toISOString().replace(/[:.]/g, "-");
      const exportFilters = liveTail ? createFiltersForRange(liveRangePreset, logFilters) : logFilters;
      const maxExportPages = 50;

      if (route.tab === "logs") {
        const pageSize = 100;
        let page = 1;
        let collected: EventRecord[] = [];
        let total = 0;

        do {
          const response = await getLogs({
            ...exportFilters,
            project_id: route.projectId,
            page: String(page),
            limit: String(pageSize)
          });
          collected = collected.concat(response.events);
          total = response.total;
          page += 1;
        } while (collected.length < total && page <= maxExportPages);

        downloadBlob(
          `${currentProject?.name ?? "project"}-logs-${timestamp}.ndjson`,
          collected.map((event) => JSON.stringify(event)).join("\n"),
          "application/x-ndjson"
        );
        setNotice(
          collected.length < total
            ? `Exported first ${collected.length} of ${total} log events.`
            : `Exported ${collected.length} log events.`
        );
        return;
      }

      if (route.tab === "traces") {
        const pageSize = 100;
        let page = 1;
        let collected: TraceResponse["traces"] = [];
        let total = 0;

        do {
          const response = await getTraces({
            project_id: route.projectId,
            from: exportFilters.from,
            to: exportFilters.to,
            page: String(page),
            limit: String(pageSize)
          });
          collected = collected.concat(response.traces);
          total = response.total;
          page += 1;
        } while (collected.length < total && page <= maxExportPages);

        downloadBlob(
          `${currentProject?.name ?? "project"}-traces-${timestamp}.json`,
          JSON.stringify(collected, null, 2),
          "application/json"
        );
        setNotice(
          collected.length < total
            ? `Exported first ${collected.length} of ${total} trace groups.`
            : `Exported ${collected.length} trace groups.`
        );
        return;
      }

      const response = await getStats({
        project_id: route.projectId,
        from: exportFilters.from,
        to: exportFilters.to,
        page: "1",
        limit: exportFilters.limit
      });
      downloadBlob(
        `${currentProject?.name ?? "project"}-stats-${timestamp}.json`,
        JSON.stringify(response, null, 2),
        "application/json"
      );
      setNotice("Exported stats snapshot.");
    } catch (exportError) {
      setError(exportError instanceof Error ? exportError.message : "Failed to export current view");
    }
  }

  function handleRefreshCurrentTab() {
    if (route.page !== "project") {
      return;
    }

    if (route.tab === "logs") {
      void loadLogs(route.projectId, logFilters, { followLatest: liveTail });
      return;
    }
    if (route.tab === "traces") {
      void loadTraces(route.projectId, logFilters);
      return;
    }
    void loadStats(route.projectId, logFilters);
  }

  function handleTablePage(nextPage: number) {
    const nextFilters = { ...logFilters, page: String(Math.max(1, nextPage)) };
    setLogFilters(nextFilters);

    if (route.page !== "project") {
      return;
    }
    if (route.tab === "logs") {
      void loadLogs(route.projectId, nextFilters);
      return;
    }
    if (route.tab === "traces") {
      void loadTraces(route.projectId, nextFilters);
    }
  }

  const curlExample = latestKey
    ? `curl -X POST ${window.location.origin}/api/ingest \\
  -H "Authorization: Bearer ${latestKey.ingestKey}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "schema_version": 1,
    "project_id": "${latestKey.project.id}",
    "kind": "log",
    "ts": "${new Date().toISOString()}",
    "source": "curl",
    "level": "info",
    "name": "hello.vigil",
    "attrs": { "route": "/first-run" },
    "body": { "message": "hello from vigil" }
  }'`
    : "";

  const filterDrawer = route.page === "project" && currentProject ? (
    <>
      <aside className={`filters-drawer ${filterDrawerOpen ? "open" : "closed"}`}>
        <div className="drawer-header">
          <div>
            <p className="eyebrow">Advanced Filters</p>
            <strong>{currentProject.name}</strong>
            <p className="drawer-help">
              Keep the common controls in the toolbar. Use this drawer for exact name match, custom window bounds, and table detail settings.
            </p>
          </div>
          <button className="secondary drawer-close" onClick={() => setFilterDrawerOpen(false)}>
            Close
          </button>
        </div>

        <div className="drawer-section">
          <span className="drawer-label">Window</span>
          <label className="field-block">
            <span>From</span>
            <input
              type="datetime-local"
              value={toLocalDateTimeValue(logFilters.from)}
              onChange={(event) => handleCustomTimeChange("from", event.target.value)}
            />
          </label>
          <label className="field-block">
            <span>To</span>
            <input
              type="datetime-local"
              value={toLocalDateTimeValue(logFilters.to)}
              onChange={(event) => handleCustomTimeChange("to", event.target.value)}
            />
          </label>
        </div>

        <div className="drawer-section">
          <span className="drawer-label">Stream</span>
          <label className="field-block">
            <span>Rows</span>
            <select value={logFilters.limit} onChange={(event) => handleFilterChange("limit", event.target.value)}>
              {limitOptions.map((option) => (
                <option key={option} value={option}>
                  {option}
                </option>
              ))}
            </select>
          </label>
        </div>

        {currentTab === "logs" ? (
          <div className="drawer-section">
            <span className="drawer-label">Log Filters</span>
            <label className="field-block">
              <span>Kind</span>
              <select value={logFilters.kind} onChange={(event) => handleFilterChange("kind", event.target.value)}>
                <option value="">All kinds</option>
                <option value="log">log</option>
                <option value="trace">trace</option>
                <option value="metric">metric</option>
              </select>
            </label>
            <label className="field-block">
              <span>Level</span>
              <select value={logFilters.level} onChange={(event) => handleFilterChange("level", event.target.value)}>
                <option value="">All levels</option>
                <option value="info">info</option>
                <option value="warn">warn</option>
                <option value="error">error</option>
              </select>
            </label>
            <label className="field-block">
              <span>Exact event name</span>
              <input
                placeholder="hello.vigil"
                value={logFilters.name}
                onChange={(event) => handleFilterChange("name", event.target.value)}
              />
              <small>Matches only the normalized event `name` field.</small>
            </label>
          </div>
        ) : null}

        <div className="drawer-footer">
          <button className="secondary" onClick={handleResetFilters}>
            Reset
          </button>
          <button onClick={handleApplyFilters}>Apply</button>
        </div>
      </aside>
      {filterDrawerOpen ? <div className="drawer-scrim" onClick={() => setFilterDrawerOpen(false)} /> : null}
    </>
  ) : null;

  return (
    <main className="app-shell">
      <header className="topbar">
        <div className="brand-block">
          <p className="eyebrow">Vigil</p>
          <h1>{route.page === "projects" ? "Projects" : currentProject?.name ?? "Explorer"}</h1>
          <p className="subtle">
            {route.page === "projects"
              ? "Local-first logs, traces, and lightweight stats."
              : `${currentProject?.id ?? ""} | ${formatRangeLabel(logFilters, timePreset)}`}
          </p>
        </div>

        <div className="topbar-actions">
          {route.page === "project" ? (
            <span className={`status-pill ${syncStatus?.stale ? "warn" : "ok"}`}>
              {syncStatus?.stale ? "Indexing lag" : "Indexed"}
            </span>
          ) : null}
          {retentionStatus?.enabled ? (
            <span className={`status-pill ${retentionStatus.dry_run ? "warn" : "ok"}`}>
              {retentionStatus.dry_run ? `Retention dry-run ${retentionStatus.days}d` : `Retention ${retentionStatus.days}d`}
            </span>
          ) : null}
          {healthError ? <span className="status-pill error">{healthError}</span> : null}
          <button className="secondary theme-toggle" onClick={() => setTheme((current) => (current === "dark" ? "light" : "dark"))}>
            {themeLabel(theme)} mode
          </button>
        </div>
      </header>

      {notice ? <section className="alert info">{notice}</section> : null}
      {error ? <section className="alert error">{error}</section> : null}
      {route.page === "project" && currentWarnings.length ? (
        <section className="alert warn">{currentWarnings.map((warning) => warning.message).join(" ")}</section>
      ) : null}
      {route.page === "project" && syncStatus?.last_error ? (
        <section className="alert error">{syncStatus.last_error}</section>
      ) : null}
      {route.page === "project" && syncStatus?.stale ? (
        <section className="alert warn">
          Raw ingest is ahead of indexed views. Latest ingested: {syncStatus.latest_ingested_at || "n/a"}.
        </section>
      ) : null}
      {latestDataOutsideWindow ? (
        <section className="alert warn alert-with-action">
          <span>
            Latest indexed data is at {formatDateTime(latestIndexedAt)}, outside {formatRangeLabel(logFilters, timePreset)}.
          </span>
          <button className="secondary" onClick={handleShowLatestIndexedData}>
            Show latest indexed data
          </button>
        </section>
      ) : null}

      {route.page === "projects" ? (
        <section className="projects-layout">
          <section className="surface create-surface">
            <div className="surface-heading">
              <div>
                <p className="eyebrow">Create Project</p>
                <strong>Start a new stream</strong>
              </div>
            </div>
            <div className="create-form">
              <input
                data-testid="project-name-input"
                placeholder="my-side-project"
                value={createName}
                onChange={(event) => setCreateName(event.target.value)}
              />
              <button data-testid="create-project-button" onClick={() => void handleCreateProject()}>
                Create
              </button>
            </div>
          </section>

          {latestKey ? (
            <section className="surface">
              <div className="surface-heading">
                <div>
                  <p className="eyebrow">Latest Ingest Key</p>
                  <strong>{latestKey.project.name}</strong>
                </div>
                <button className="secondary" onClick={() => openProject(latestKey.project.id)}>
                  Open project
                </button>
              </div>
              <pre className="code-block" data-testid="latest-ingest-key">
                {latestKey.ingestKey}
              </pre>
              <pre className="code-block code-block-spaced">{curlExample}</pre>
            </section>
          ) : null}

          <section className="surface">
            <div className="surface-heading">
              <div>
                <p className="eyebrow">Projects</p>
                <strong>{projects.length} configured</strong>
              </div>
            </div>

            {loading.projects ? <p className="muted">Loading projects...</p> : null}
            {!projects.length && !loading.projects ? (
              <div className="empty-state">
                <strong>No projects yet</strong>
                <p className="muted">Create one above, copy its key, and send the first event.</p>
              </div>
            ) : null}

            {projects.length ? (
              <div className="table-shell">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>Project</th>
                      <th>Status</th>
                      <th>Updated</th>
                      <th>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {projects.map((project) => (
                      <tr key={project.id}>
                        <td>
                          <div className="table-primary">
                            <strong>{project.name}</strong>
                            <span>{project.id}</span>
                          </div>
                        </td>
                        <td>
                          <span className="status-pill ok">{project.status}</span>
                        </td>
                        <td>{formatDateTime(project.updated_at)}</td>
                        <td>
                          <div className="inline-actions">
                            <button className="secondary" onClick={() => void handleRegenerate(project.id)}>
                              Rotate key
                            </button>
                            <button onClick={() => openProject(project.id)}>Open</button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : null}
          </section>
        </section>
      ) : null}

      {route.page === "project" && currentProject ? (
        <section className="project-layout">
          {filterDrawer}

          <div className="project-main">
            <section className="surface project-header">
              <div className="project-header-main">
                <div className="project-header-top">
                  <button
                    className="secondary"
                    onClick={() => {
                      setRoute({ page: "projects" });
                      navigate({ page: "projects" });
                    }}
                  >
                    All projects
                  </button>
                </div>

                <div className="project-title-row">
                  <div>
                    <h2>{currentProject.name}</h2>
                    <p className="muted">{currentProject.id}</p>
                  </div>
                  <div className="inline-actions">
                    <button className="secondary" onClick={() => void handleRegenerate(currentProject.id)}>
                      Rotate ingest key
                    </button>
                  </div>
                </div>

                <div className="project-meta-grid">
                  <div className="meta-tile">
                    <span>Status</span>
                    <strong>{currentProject.status}</strong>
                  </div>
                  <div className="meta-tile">
                    <span>Created</span>
                    <strong>{formatDateTime(currentProject.created_at)}</strong>
                  </div>
                  <div className="meta-tile">
                    <span>Updated</span>
                    <strong>{formatDateTime(currentProject.updated_at)}</strong>
                  </div>
                  <div className="meta-tile">
                    <span>Window</span>
                    <strong>{formatRangeLabel(logFilters, timePreset)}</strong>
                  </div>
                </div>
              </div>

              <nav className="tab-strip">
                {(["logs", "traces", "stats"] as Tab[]).map((tab) => (
                  <button
                    key={tab}
                    className={`tab-button ${currentTab === tab ? "active" : ""}`}
                    data-testid={`tab-${tab}`}
                    onClick={() => changeTab(tab)}
                  >
                    <span>{tab}</span>
                    <strong>
                      {tab === "logs" ? logs?.total ?? 0 : tab === "traces" ? traces?.total ?? 0 : stats?.total_events ?? 0}
                    </strong>
                  </button>
                ))}
              </nav>
            </section>

            {latestKey?.project.id === currentProject.id ? (
              <section className="surface">
                <div className="surface-heading">
                  <div>
                    <p className="eyebrow">Latest Ingest Key</p>
                    <strong>Shown once</strong>
                  </div>
                </div>
                <pre className="code-block" data-testid="latest-ingest-key">
                  {latestKey.ingestKey}
                </pre>
                <pre className="code-block code-block-spaced">{curlExample}</pre>
              </section>
            ) : null}

            <section className="surface toolbar-surface">
              <div className="toolbar-primary">
                <div>
                  <p className="eyebrow">{currentTab}</p>
                  <strong>{currentTotal} matching records</strong>
                  <p className="muted">
                    {liveTail ? "Live tail is active for this window." : `Showing ${formatRangeLabel(logFilters, timePreset)}.`}
                  </p>
                </div>
                <div className="toolbar-controls">
                  {currentTab === "logs" ? (
                    <label className="toolbar-search">
                      <span>Search</span>
                      <input
                        placeholder="search message, source, attrs, body, or event name"
                        value={searchText}
                        onChange={(event) => setSearchText(event.target.value)}
                        onKeyDown={(event) => {
                          if (event.key === "Enter") {
                            handleApplyFilters();
                          }
                        }}
                      />
                    </label>
                  ) : null}
                  <div className="toolbar-window">
                    <span>Window</span>
                    <div className="segmented-group">
                      {quickRangeOptions.map((option) => (
                        <button
                          key={option.value}
                          className={`secondary segmented-button ${timePreset === option.value ? "active" : ""}`}
                          onClick={() => handleRangePresetChange(option.value)}
                        >
                          {option.label}
                        </button>
                      ))}
                    </div>
                  </div>
                  <button className={`secondary toggle-button ${liveTail ? "active" : ""}`} onClick={handleToggleLiveTail}>
                    {liveTail ? "Live tail on" : "Live tail off"}
                  </button>
                </div>
              </div>
              <div className="inline-actions">
                <button className="secondary" onClick={() => setFilterDrawerOpen(true)}>
                  Filters
                </button>
                <button className="secondary" onClick={() => void handleCopyShareUrl()}>
                  Copy link
                </button>
                <button className="secondary" onClick={() => void handleExportCurrentView()}>
                  Export
                </button>
                <button data-testid="refresh-current-tab" onClick={handleRefreshCurrentTab}>
                  {currentTab === "logs" ? "Refresh table" : "Refresh"}
                </button>
              </div>
            </section>

            {currentTab === "logs" ? (
              <section className="logs-layout">
                <section className="surface table-surface">
                  <div className="table-shell" data-testid="logs-table-shell">
                    {loading.logs ? <p className="muted table-state">Loading logs...</p> : null}
                    {!logs?.events.length && !loading.logs ? <p className="muted table-state">No events for this filter.</p> : null}

                    {logs?.events.length ? (
                      <table className="data-table log-table">
                        <thead>
                          <tr>
                            <th>Time</th>
                            <th>Level</th>
                            <th>Kind</th>
                            <th>Source</th>
                            <th>Event</th>
                            <th>Message</th>
                            <th>Trace</th>
                          </tr>
                        </thead>
                        <tbody>
                          {logs.events.map((event) => (
                            <tr
                              key={event.event_id}
                              className={selectedEvent?.event_id === event.event_id ? "selected" : ""}
                              onClick={() => setSelectedEvent(event)}
                            >
                              <td>{formatTime(event.ts)}</td>
                              <td>
                                <span className={`tone-badge tone-${event.level ?? "info"}`}>{event.level ?? "info"}</span>
                              </td>
                              <td>
                                <span className={`tone-badge tone-kind-${event.kind}`}>{event.kind}</span>
                              </td>
                              <td>{event.source}</td>
                              <td>
                                <div className="table-primary">
                                  <strong>{event.name}</strong>
                                  <span>{eventInlineFields(event).join("  ") || "structured event"}</span>
                                </div>
                              </td>
                              <td className="message-cell">{eventMessage(event)}</td>
                              <td>{shortId(event.trace_id)}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    ) : null}
                  </div>

                  <div className="pagination">
                    <button
                      className="secondary"
                      disabled={!logs || logs.page <= 1}
                      onClick={() => handleTablePage((logs?.page ?? 1) - 1)}
                    >
                      Previous
                    </button>
                    <span>
                      Page {logs?.page ?? 1} / {logs ? Math.max(1, Math.ceil(logs.total / logs.limit)) : 1}
                    </span>
                    <button
                      className="secondary"
                      disabled={!logs || logs.page * logs.limit >= logs.total}
                      onClick={() => handleTablePage((logs?.page ?? 1) + 1)}
                    >
                      Next
                    </button>
                  </div>
                </section>

                <aside className="surface detail-surface">
                  <div className="surface-heading">
                    <div>
                      <p className="eyebrow">Selected Event</p>
                      <strong>{selectedEvent?.name ?? "No selection"}</strong>
                    </div>
                    {selectedEvent ? <span className="muted">{selectedEvent.event_id}</span> : null}
                  </div>

                  {selectedEvent ? (
                    <div className="event-detail">
                      <div className="detail-grid">
                        <div className="meta-tile">
                          <span>Time</span>
                          <strong>{formatDateTime(selectedEvent.ts)}</strong>
                        </div>
                        <div className="meta-tile">
                          <span>Source</span>
                          <strong>{selectedEvent.source}</strong>
                        </div>
                        <div className="meta-tile">
                          <span>Level</span>
                          <strong>{selectedEvent.level || "info"}</strong>
                        </div>
                        <div className="meta-tile">
                          <span>Trace</span>
                          <strong>{selectedEvent.trace_id || "n/a"}</strong>
                        </div>
                      </div>

                      <div className="detail-block">
                        <span className="drawer-label">Message</span>
                        <p className="detail-message">{eventMessage(selectedEvent)}</p>
                      </div>

                      <div className="detail-block">
                        <span className="drawer-label">Attrs</span>
                        <pre className="code-block">{JSON.stringify(selectedEvent.attrs, null, 2)}</pre>
                      </div>

                      <div className="detail-block">
                        <span className="drawer-label">Body</span>
                        <pre className="code-block">{JSON.stringify(selectedEvent.body, null, 2)}</pre>
                      </div>
                    </div>
                  ) : (
                    <p className="muted">Select a row to inspect the normalized event payload.</p>
                  )}
                </aside>
              </section>
            ) : null}

            {currentTab === "traces" ? (
              <section className="surface">
                {loading.traces ? <p className="muted">Loading traces...</p> : null}
                {!traces?.traces.length && !loading.traces ? <p className="muted">No trace events yet.</p> : null}

                <div className="trace-list">
                  {traces?.traces.map((trace) => (
                    <article key={trace.trace_id} className="trace-card">
                      <div className="trace-card-header">
                        <div>
                          <strong>{trace.name || trace.trace_id}</strong>
                          <p className="muted">
                            {trace.event_count} events | {trace.trace_id}
                          </p>
                        </div>
                        <span className="muted">{formatDateTime(trace.started_at)}</span>
                      </div>
                      <div className="trace-events">
                        {trace.events.map((event) => (
                          <div key={event.event_id} className="trace-row">
                            <span>{formatTime(event.ts)}</span>
                            <strong>{event.name}</strong>
                            <span>{event.source}</span>
                            <span>{event.level || "info"}</span>
                          </div>
                        ))}
                      </div>
                    </article>
                  ))}
                </div>

                <div className="pagination">
                  <button
                    className="secondary"
                    disabled={!traces || traces.page <= 1}
                    onClick={() => handleTablePage((traces?.page ?? 1) - 1)}
                  >
                    Previous
                  </button>
                  <span>
                    Page {traces?.page ?? 1} / {traces ? Math.max(1, Math.ceil(traces.total / traces.limit)) : 1}
                  </span>
                  <button
                    className="secondary"
                    disabled={!traces || traces.page * traces.limit >= traces.total}
                    onClick={() => handleTablePage((traces?.page ?? 1) + 1)}
                  >
                    Next
                  </button>
                </div>
              </section>
            ) : null}

            {currentTab === "stats" ? (
              <section className="stats-layout">
                <section className="surface stats-grid">
                  <div className="stats-card">
                    <span>Total events</span>
                    <strong data-testid="stats-total-events">{stats?.total_events ?? 0}</strong>
                  </div>
                  <div className="stats-card">
                    <span>Token total</span>
                    <strong>{stats?.token_total ?? 0}</strong>
                  </div>
                  <div className="stats-card">
                    <span>Cost total</span>
                    <strong>{stats?.cost_total ?? 0}</strong>
                  </div>
                </section>

                <section className="stats-columns">
                  <section className="surface">
                    <div className="surface-heading">
                      <div>
                        <p className="eyebrow">Kinds</p>
                        <strong>By event kind</strong>
                      </div>
                    </div>
                    <ul className="stat-list">
                      {stats?.by_kind.map((entry) => (
                        <li key={entry.label}>
                          <span>{entry.label}</span>
                          <strong>{entry.count}</strong>
                        </li>
                      ))}
                    </ul>
                  </section>

                  <section className="surface">
                    <div className="surface-heading">
                      <div>
                        <p className="eyebrow">Levels</p>
                        <strong>Errors and levels</strong>
                      </div>
                    </div>
                    <ul className="stat-list">
                      {stats?.by_level.map((entry) => (
                        <li key={entry.label}>
                          <span>{entry.label}</span>
                          <strong>{entry.count}</strong>
                        </li>
                      ))}
                    </ul>
                  </section>

                  <section className="surface">
                    <div className="surface-heading">
                      <div>
                        <p className="eyebrow">Volume</p>
                        <strong>Recent daily volume</strong>
                      </div>
                    </div>
                    <ul className="stat-list">
                      {stats?.volume.map((entry) => (
                        <li key={entry.day}>
                          <span>{entry.day}</span>
                          <strong>{entry.count}</strong>
                        </li>
                      ))}
                    </ul>
                  </section>
                </section>
              </section>
            ) : null}
          </div>
        </section>
      ) : null}
    </main>
  );
}
