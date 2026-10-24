# Agent Traces

Vigil records AI agent work as **runs** with ordered **steps**, alongside your
logs, traces, and metrics. This page covers the model, the ingestion contract,
the SDK helpers, and the Pi producer.

## Mental model

Vigil separates hard boundaries from soft dimensions:

- **Project** — the hard boundary. Isolation, ingest keys, retention, and the data
  directory all belong to a project.
- **Run** — one agent invocation (a task, session, or REST-triggered agent call).
- **Step** — one ordered event inside a run: a prompt, model decision, tool call,
  tool result, and so on.
- **Dimensions** — `source`, `environment`, `model`, `run`, and end-user values that
  you filter on within a project.

Do not create one project per agent session. Create a project per product, team,
or retention boundary, then slice behavior with runs and dimensions.

```
Project
  ├─ Signals: log · trace · metric · run · run_step
  ├─ Correlation: trace_id / span_id span frontend → backend → agent
  │               run_id groups one agent invocation's ordered steps
  └─ Dimensions: source, model, run, session, end_user
```

## The run and step contract

Runs and steps use the same native event envelope as every other signal. The
`kind` is `run` (lifecycle) or `run_step` (an ordered step).

| Field | Where | Meaning |
|---|---|---|
| `run_id` | run + step | Identifies one agent invocation. Required for run events. |
| `step_type` | step | `prompt`, `decision`, `tool_call`, `tool_result`, `model_change`, `thinking_change`, or a custom value. |
| `trace_id` | run + step | Correlates the run with app traces and logs in the same project. |
| `span_id` | step | Correlates one step with one app span. |
| `source` | run + step | Producer label, such as `pi`, `my-app`, or `support-agent`. |
| `level` | run + step | Standard level; `error` for failed steps. |
| `attrs` | run + step | Structured metadata (see below). |
| `body` | run + step | Replay content: thinking, text, tool arguments, output. |

Common `attrs` keys the read model understands:

- `status` — `running`, `succeeded`, `failed`, `canceled`
- `model` — model name
- `total_tokens`, `cost_usd` — usage totals
- `duration_ms` — step duration
- `failure_class`, `outcome` — run outcome
- nested `run` object with `repo`, `branch`, `worktree`, `model`, `prompt`

### Ingest a run

```sh
curl -X POST http://localhost:8080/api/ingest \
  -H "Authorization: Bearer vigil_..." \
  -H 'Content-Type: application/json' \
  -d '{
    "schema_version": 1,
    "project_id": "proj_...",
    "kind": "run",
    "run_id": "run_123",
    "trace_id": "trace_abc",
    "ts": "2026-05-24T12:00:00Z",
    "source": "my-app",
    "name": "support-agent",
    "attrs": { "run": { "model": "gpt-4o" }, "status": "running", "prompt": "find order 42" }
  }'
```

### Ingest a step

```sh
curl -X POST http://localhost:8080/api/ingest \
  -H "Authorization: Bearer vigil_..." \
  -H 'Content-Type: application/json' \
  -d '{
    "schema_version": 1,
    "project_id": "proj_...",
    "kind": "run_step",
    "run_id": "run_123",
    "step_type": "tool_call",
    "trace_id": "trace_abc",
    "span_id": "span_1",
    "ts": "2026-05-24T12:00:01Z",
    "source": "my-app",
    "name": "search_orders",
    "attrs": { "status": "success", "duration_ms": 18 },
    "body": { "args": { "user_id": 42 } }
  }'
```

The read model aggregates `step_count`, `total_tokens`, `total_cost_usd`, and
`error_count` per run, and tracks start/end times. Finish the run with a final
`kind=run` event whose `attrs.status` is `succeeded`, `failed`, or `canceled`.

## SDK helpers

The Python and TypeScript SDKs provide an `agent_run` helper that manages run
lifecycle and correlation. Pass the app's `trace_id` to link the run to the
request that triggered it.

### Python

```python
from vigil_sdk import VigilClient

vigil = VigilClient.from_env()

with vigil.agent_run("support-agent", trace_id=request_trace_id, source="my-app", prompt="find order 42") as run:
    run.decision(thought="look up the order", model="gpt-4o", tokens=820, cost_usd=0.004)
    run.tool_call("search_orders", {"user_id": 42})
    run.tool_result("search_orders", status="success", duration_ms=18)
```

The context manager emits `run.started`, then `run.completed` on success or
`run.failed` (with `failure_class`) when the block raises. `run.finish(...)` is
idempotent if you want to end the run explicitly.

### TypeScript

```ts
import { VigilClient } from "vigil-observability";

const vigil = VigilClient.fromEnv();
const run = await vigil.agentRun("support-agent", { traceId: requestTraceId, source: "my-app" });

await run.decision({ thought: "look up the order", model: "gpt-4o", tokens: 820, costUsd: 0.004 });
await run.toolCall("search_orders", { userId: 42 });
await run.toolResult("search_orders", { status: "success", durationMs: 18 });
await run.finish();
```

## Pi producer

`integrations/pi/vigil.ts` streams every Pi coding-agent session into Vigil:

```sh
cp integrations/pi/vigil.ts ~/.pi/agent/extensions/vigil.ts
```

It emits one run per session with steps for `prompt`, `decision`
(thinking + text + tokens/cost), `tool_call`, `tool_result`, `model_change`, and
`thinking_change`.

### Project resolution

Producers resolve the target project in this order:

1. process environment (`VIGIL_BASE_URL`, `VIGIL_PROJECT_ID`, `VIGIL_INGEST_KEY`)
2. nearest `.env` walking up from the working directory
3. the global `vigil init` / `vigil use` config

So a folder that has a `.env` owns its project, and everything else falls back to
the global project.

### Switching project for one session

In a Pi session:

```txt
/vigil-project                       # list projects and pick one
/vigil-project <project_id> <key>    # switch directly
```

Switching mid-session starts a new run in the new project, because `run_id` is
globally unique. Switching before the first prompt is cleanest.

### Producer environment variables

| Variable | Default | Meaning |
|---|---|---|
| `VIGIL_AGENT_DISABLED` | `false` | Disable the Pi producer |
| `VIGIL_SOURCE` | `pi` | Run source label |
| `VIGIL_CAPTURE_THINKING` | `true` | Capture model reasoning |
| `VIGIL_CAPTURE_TEXT` | `true` | Capture model text/plans |
| `VIGIL_CAPTURE_TOOL_ARGS` | `true` | Capture tool arguments |
| `VIGIL_CAPTURE_TOOL_OUTPUT` | `false` | Capture truncated tool output |

Sensitive-looking keys are redacted client-side, and Vigil redacts again before
raw append.

## Correlation

A single `trace_id` can span a frontend event, a backend request, and the agent
run it triggered, all inside one project:

- Runs and steps store `trace_id` / `span_id`.
- The **Runs** tab links each step to its trace.
- The **Traces** tab shows agent steps inline and links them back to their run.
- Pi sessions set `trace_id = run_id`, so a session also appears as a trace.

## API

`GET /api/runs`

Query params: `project_id`, `from`, `to`, `page`, `limit`.

`GET /api/runs/{run_id}`

Returns the run summary plus its ordered steps.

## Storage and rebuild

Runs are stored raw in the same append-only NDJSON as every other event, then
promoted into `runs` and `run_steps` SQLite read models by the async indexer.
`ResetReadModels` clears them and raw replay repopulates them, so runs are always
rebuildable from the source of truth.

## Next producers

The contract is provider-agnostic. Pi is the first adapter; app-hosted agents use
the SDK helpers, and future adapters (Claude Code hooks, Codex, OpenTelemetry
exporters using `vigil.*` attributes) speak the same run and step contract.
