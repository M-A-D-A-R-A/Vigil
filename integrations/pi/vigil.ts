/**
 * Vigil agent-trace producer for Pi.
 *
 * Streams one agent "run" per Pi session into Vigil, with ordered steps for the
 * user prompt, model decisions (thinking + text + token/cost), tool calls, and
 * tool results.
 *
 * Install:
 *   cp integrations/pi/vigil.ts ~/.pi/agent/extensions/vigil.ts
 *
 * Configuration (env, falling back to the `vigil init` CLI config):
 *   VIGIL_BASE_URL, VIGIL_PROJECT_ID, VIGIL_INGEST_KEY
 *
 * Optional:
 *   VIGIL_AGENT_DISABLED=1        disable the producer
 *   VIGIL_SOURCE=pi              run source label
 *   VIGIL_CAPTURE_THINKING       capture model reasoning (default: true)
 *   VIGIL_CAPTURE_TEXT           capture model text/plans (default: true)
 *   VIGIL_CAPTURE_TOOL_ARGS      capture tool arguments (default: true)
 *   VIGIL_CAPTURE_TOOL_OUTPUT    capture truncated tool output (default: false)
 */

import type { ExtensionAPI, ExtensionCommandContext } from "@earendil-works/pi-coding-agent";
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join, parse } from "node:path";

const THINKING_LIMIT = 4000;
const TEXT_LIMIT = 4000;
const ARGS_LIMIT = 4000;
const OUTPUT_LIMIT = 2000;
const MAX_FAILURES = 5;

type VigilConfig = {
  baseUrl: string;
  projectId: string;
  ingestKey: string;
  source: string;
};

type StepType = "prompt" | "decision" | "tool_call" | "tool_result" | "model_change" | "thinking_change";

function envFlag(name: string, fallback: boolean): boolean {
  const raw = process.env[name];
  if (raw === undefined || raw === "") return fallback;
  return /^(1|true|yes|on)$/i.test(raw.trim());
}

function defaultCliConfigPath(): string {
  if (process.platform === "darwin") {
    return join(homedir(), "Library", "Application Support", "vigil", "config.json");
  }
  if (process.platform === "win32") {
    return join(process.env.APPDATA ?? homedir(), "vigil", "config.json");
  }
  return join(homedir(), ".config", "vigil", "config.json");
}

function parseDotEnv(content: string): Record<string, string> {
  const values: Record<string, string> = {};
  for (const line of content.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const eq = trimmed.indexOf("=");
    if (eq <= 0) continue;
    const key = trimmed.slice(0, eq).trim();
    let value = trimmed.slice(eq + 1).trim();
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }
    if (key) values[key] = value;
  }
  return values;
}

function findNearestEnv(startDir: string): Record<string, string> {
  let dir = startDir;
  const root = parse(dir).root;
  while (true) {
    const candidate = join(dir, ".env");
    if (existsSync(candidate)) {
      try {
        return parseDotEnv(readFileSync(candidate, "utf8"));
      } catch {
        // ignore unreadable .env
      }
    }
    if (dir === root) return {};
    dir = dirname(dir);
  }
}

function loadCliConfig(): Record<string, string> {
  const path = process.env.VIGIL_CONFIG_PATH || defaultCliConfigPath();
  try {
    return JSON.parse(readFileSync(path, "utf8")) as Record<string, string>;
  } catch {
    return {};
  }
}

/**
 * Resolve the Vigil target for a directory:
 *   1. process env
 *   2. nearest .env walking up from the working directory
 *   3. global CLI config (`vigil init` / `vigil use`)
 */
function resolveConfig(startDir: string): VigilConfig | null {
  if (envFlag("VIGIL_AGENT_DISABLED", false)) return null;

  const env = process.env;
  const nearest = findNearestEnv(startDir);
  const cli = loadCliConfig();
  const pick = (envKey: string, cliKey: string): string =>
    (env[envKey] as string | undefined) || nearest[envKey] || (cli[cliKey] as string | undefined) || "";

  const baseUrl = pick("VIGIL_BASE_URL", "server_url");
  const projectId = pick("VIGIL_PROJECT_ID", "active_project_id");
  const ingestKey = pick("VIGIL_INGEST_KEY", "ingest_key");
  if (!baseUrl || !projectId || !ingestKey) return null;

  return {
    baseUrl: baseUrl.replace(/\/+$/, ""),
    projectId,
    ingestKey,
    source: (env.VIGIL_SOURCE as string | undefined) || "pi",
  };
}

function truncate(value: string, limit: number): string {
  if (value.length <= limit) return value;
  return `${value.slice(0, limit)}… [truncated ${value.length - limit} chars]`;
}

function isSensitiveKey(key: string): boolean {
  const normalized = key.toLowerCase().replace(/[^a-z0-9]/g, "");
  if (!normalized) return false;
  if (SENSITIVE_KEYS.has(normalized)) return true;
  return (
    normalized.endsWith("token") ||
    normalized.endsWith("secret") ||
    normalized.includes("authorization") ||
    normalized.includes("cookie")
  );
}

const SENSITIVE_KEYS = new Set([
  "password",
  "passwd",
  "pwd",
  "token",
  "secret",
  "apikey",
  "xapikey",
  "authorization",
  "cookie",
  "setcookie",
  "accesstoken",
  "refreshtoken",
  "idtoken",
  "clientsecret",
  "privatekey",
]);

function redactValue(value: unknown, depth = 0): unknown {
  if (depth > 8) return "[redacted:depth]";
  if (Array.isArray(value)) return value.map((item) => redactValue(item, depth + 1));
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
      out[key] = isSensitiveKey(key) ? "[REDACTED]" : redactValue(child, depth + 1);
    }
    return out;
  }
  return value;
}

/** Redact object keys first, then keep the result structured unless it is too large. */
function boundedRedacted(value: unknown, limit: number): unknown {
  const redacted = redactValue(value ?? {});
  const json = JSON.stringify(redacted);
  if (json.length <= limit) return redacted;
  return truncate(json, limit);
}

function modelId(model: unknown): string {
  if (!model || typeof model !== "object") return "";
  const record = model as Record<string, unknown>;
  for (const key of ["id", "name", "modelId"]) {
    if (typeof record[key] === "string" && (record[key] as string).trim() !== "") {
      return (record[key] as string).trim();
    }
  }
  return "";
}

function gitInfo(cwd: string): { root: string; branch: string } {
  const run = (args: string[]): string => {
    try {
      return execFileSync("git", args, { cwd, timeout: 800, stdio: ["ignore", "pipe", "ignore"] })
        .toString()
        .trim();
    } catch {
      return "";
    }
  };
  return {
    root: run(["rev-parse", "--show-toplevel"]),
    branch: run(["rev-parse", "--abbrev-ref", "HEAD"]),
  };
}

function usageNumbers(usage: unknown): { tokens: number; cost: number; input: number; output: number; reasoning: number } {
  const record = (usage ?? {}) as Record<string, any>;
  const input = Number(record.input ?? 0) || 0;
  const output = Number(record.output ?? 0) || 0;
  const reasoning = Number(record.reasoning ?? 0) || 0;
  const tokens = Number(record.totalTokens ?? input + output) || 0;
  const cost = Number(record.cost?.total ?? 0) || 0;
  return { tokens, cost, input, output, reasoning };
}

function messageContent(message: any): { text: string; thinking: string; toolCalls: Array<{ id: string; name: string }> } {
  const content = Array.isArray(message?.content) ? message.content : [];
  let text = "";
  let thinking = "";
  const toolCalls: Array<{ id: string; name: string }> = [];
  for (const block of content) {
    if (block?.type === "text" && typeof block.text === "string") text += block.text;
    else if (block?.type === "thinking" && typeof block.thinking === "string") thinking += block.thinking;
    else if (block?.type === "toolCall") toolCalls.push({ id: String(block.id ?? ""), name: String(block.name ?? "") });
  }
  return { text, thinking, toolCalls };
}

export default function (pi: ExtensionAPI) {
  if (envFlag("VIGIL_AGENT_DISABLED", false)) return;

  let config = resolveConfig(process.cwd());

  const captureThinking = envFlag("VIGIL_CAPTURE_THINKING", true);
  const captureText = envFlag("VIGIL_CAPTURE_TEXT", true);
  const captureToolArgs = envFlag("VIGIL_CAPTURE_TOOL_ARGS", true);
  const captureToolOutput = envFlag("VIGIL_CAPTURE_TOOL_OUTPUT", false);

  let runId = randomUUID();
  let sessionId = runId;
  let currentModel = "";
  let currentThinking = "";
  let failures = 0;
  const toolStarts = new Map<string, { at: number; name: string; args: unknown }>();

  let postChain: Promise<void> = Promise.resolve();

  const doPost = async (body: Record<string, unknown>): Promise<void> => {
    if (!config) return;
    if (failures >= MAX_FAILURES) return;
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 1500);
    try {
      const response = await fetch(`${config.baseUrl}/api/ingest`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${config.ingestKey}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify(body),
        signal: controller.signal,
      });
      if (!response.ok) throw new Error(`vigil ingest ${response.status}`);
      failures = 0;
    } catch {
      failures += 1;
    } finally {
      clearTimeout(timeout);
    }
  };

  // Serialize posts so run steps keep their true order even within one millisecond.
  const post = (body: Record<string, unknown>): Promise<void> => {
    postChain = postChain.then(() => doPost(body));
    return postChain;
  };

  const envelope = (kind: "run" | "run_step", name: string, stepType: StepType | "", attrs: Record<string, unknown>, body?: unknown, level = "info") => ({
    schema_version: 1,
    project_id: config?.projectId ?? "",
    kind,
    run_id: runId,
    trace_id: runId,
    step_type: stepType || undefined,
    ts: new Date().toISOString(),
    source: config?.source ?? "pi",
    level,
    name,
    attrs: redactValue(attrs),
    body: body === undefined ? undefined : redactValue(body),
  });

  const step = (name: string, stepType: StepType, attrs: Record<string, unknown>, body?: unknown, level = "info") => {
    void post(envelope("run_step", name, stepType, attrs, body, level));
  };

  pi.on("session_start", async (_event, ctx) => {
    if (!config) return;
    try {
      runId = ctx.sessionManager?.getSessionId?.() ?? randomUUID();
    } catch {
      runId = randomUUID();
    }
    sessionId = runId;
    currentModel = modelId(ctx.model);
    currentThinking = String(ctx.thinkingLevel ?? "");
    const cwd = ctx.cwd ?? process.cwd();
    const git = gitInfo(cwd);
    let sessionName = "";
    try {
      sessionName = String(ctx.sessionManager?.getSessionName?.() ?? "");
    } catch {
      sessionName = "";
    }
    await post(
      envelope(
        "run",
        sessionName || "pi.session",
        "",
        {
          run: {
            repo: git.root || cwd,
            branch: git.branch,
            worktree: git.root || cwd,
            model: currentModel,
            cwd,
          },
          status: "running",
          session_name: sessionName,
        },
      ),
    );
  });

  pi.on("model_select", (event) => {
    currentModel = modelId(event.model) || currentModel;
    step("model.selected", "model_change", { model: currentModel });
  });

  pi.on("thinking_level_select", (event) => {
    currentThinking = String(event.level ?? "");
    step("thinking.selected", "thinking_change", { thinking_level: currentThinking });
  });

  pi.on("before_agent_start", (event) => {
    const prompt = typeof event.prompt === "string" ? event.prompt : "";
    step(
      "user.prompt",
      "prompt",
      { prompt_chars: prompt.length, model: currentModel, thinking_level: currentThinking },
      captureText ? { message: truncate(prompt, TEXT_LIMIT) } : undefined,
    );
  });

  pi.on("tool_execution_start", (event) => {
    toolStarts.set(event.toolCallId, { at: Date.now(), name: event.toolName, args: event.args });
    step(
      "tool.call",
      "tool_call",
      {
        tool: event.toolName,
        tool_call_id: event.toolCallId,
        parent_tool_call_id: event.parentToolCallId,
        status: "running",
      },
      captureToolArgs ? { args: boundedRedacted(event.args, ARGS_LIMIT) } : undefined,
    );
  });

  pi.on("tool_execution_end", (event) => {
    const started = toolStarts.get(event.toolCallId);
    toolStarts.delete(event.toolCallId);
    const durationMs = started ? Date.now() - started.at : 0;
    const status = event.isError ? "error" : "success";
    const { tokens, cost } = usageNumbers((event as any).result?.usage);
    step(
      "tool.result",
      "tool_result",
      {
        tool: event.toolName,
        tool_call_id: event.toolCallId,
        parent_tool_call_id: event.parentToolCallId,
        status,
        duration_ms: durationMs,
        total_tokens: tokens || undefined,
        cost_usd: cost || undefined,
      },
      captureToolOutput ? { message: boundedRedacted(event.result, OUTPUT_LIMIT) } : undefined,
      event.isError ? "error" : "info",
    );
  });

  pi.on("message_end", (event) => {
    const message = event.message as any;
    if (message?.role !== "assistant") return;
    const { text, thinking, toolCalls } = messageContent(message);
    const { tokens, cost, input, output, reasoning } = usageNumbers(message.usage);
    const body: Record<string, unknown> = {};
    if (captureThinking && thinking) body.thinking = truncate(thinking, THINKING_LIMIT);
    if (captureText && text) body.text = truncate(text, TEXT_LIMIT);
    if (text) body.plan = truncate(text, TEXT_LIMIT);
    step(
      "assistant.decision",
      "decision",
      {
        model: message.model ?? currentModel,
        provider: message.provider,
        thinking_level: message.thinkingLevel ?? currentThinking,
        stop_reason: message.stopReason,
        tool_calls: toolCalls.map((call) => call.name),
        tool_call_count: toolCalls.length,
        total_tokens: tokens || undefined,
        input_tokens: input || undefined,
        output_tokens: output || undefined,
        reasoning_tokens: reasoning || undefined,
        cost_usd: cost || undefined,
      },
      Object.keys(body).length > 0 ? body : undefined,
    );
  });

  pi.on("session_shutdown", async (event) => {
    if (!config) return;
    await post(
      envelope("run", "pi.session.completed", "", {
        status: "succeeded",
        outcome: `session ${event.reason}`,
      }),
    );
  });

  /**
   * `/vigil-project` shows or switches the Vigil project for the current session.
   * Default is the global CLI project; use this to send one session elsewhere.
   */
  pi.registerCommand("vigil-project", {
    description: "Show or switch the Vigil project for this session",
    handler: async (args, ctx) => {
      const baseUrl = (config?.baseUrl || (process.env.VIGIL_BASE_URL as string | undefined) || loadCliConfig().server_url || "").replace(
        /\/+$/,
        "",
      );
      if (!baseUrl) {
        ctx.ui.notify("Vigil is not configured (no base URL).", "error");
        return;
      }

      const trimmed = args.trim();
      if (trimmed) {
        const [projectArg, keyArg] = trimmed.split(/\s+/);
        await applyProject(projectArg, keyArg, baseUrl, ctx);
        return;
      }

      const projects = await listProjects(baseUrl);
      const current = config ? `current: ${config.projectId}` : "current: none";
      const labels = projects.map((project) => `${project.name} (${project.id})`);
      if (projects.length && ctx.hasUI) {
        const choice = await ctx.ui.select(`Vigil project (${current})`, labels);
        if (!choice) return;
        const selected = projects[labels.indexOf(choice)];
        if (selected) await applyProject(selected.id, undefined, baseUrl, ctx, selected.name);
        return;
      }
      ctx.ui.notify(`Vigil projects: ${labels.join(", ") || "none"} | ${current}`, "info");
    },
  });

  async function listProjects(baseUrl: string): Promise<Array<{ id: string; name: string }>> {
    try {
      const response = await fetch(`${baseUrl}/api/projects`);
      if (!response.ok) return [];
      const body = (await response.json()) as { projects?: Array<{ id: string; name: string }> };
      return (body.projects ?? []).map((project) => ({ id: String(project.id), name: String(project.name) }));
    } catch {
      return [];
    }
  }

  async function applyProject(
    projectArg: string,
    keyArg: string | undefined,
    baseUrl: string,
    ctx: ExtensionCommandContext,
    knownName?: string,
  ): Promise<void> {
    const cli = loadCliConfig();
    let projectId = projectArg;
    let projectName = knownName ?? "";
    let key = keyArg ?? "";

    if (!projectId.startsWith("proj_")) {
      const match = (await listProjects(baseUrl)).find((project) => project.name === projectId || project.id === projectId);
      if (match) {
        projectId = match.id;
        projectName = match.name;
      }
    }

    if (!key && cli.active_project_id === projectId && cli.ingest_key) key = cli.ingest_key;
    if (!key && config?.projectId === projectId) key = config.ingestKey;
    if (!key && ctx.hasUI) {
      key = (await ctx.ui.input(`Ingest key for ${projectName || projectId}`, "vigil_...")) ?? "";
    }
    if (!key) {
      ctx.ui.notify("An ingest key is required to switch projects. Pass it as the second argument.", "warning");
      return;
    }

    const previousProject = config?.projectId;
    config = { baseUrl, projectId, ingestKey: key, source: config?.source ?? "pi" };
    if (previousProject && previousProject !== projectId) {
      // run_id is globally unique, so a project switch starts a new run in the new project.
      runId = `${sessionId}:${projectId}`;
      await post(envelope("run", "pi.session", "", { status: "running", session_name: projectName || projectId }));
    }
    ctx.ui.notify(`Vigil session project set to ${projectName || projectId}.`, "info");
  }
}
