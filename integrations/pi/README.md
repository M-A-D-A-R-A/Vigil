# Vigil Pi Producer

Streams Pi coding-agent sessions into Vigil as agent **runs** and **run steps**.

One run is created per Pi session. Each run carries ordered steps:

| Step type | Source event | What it captures |
|---|---|---|
| `prompt` | `before_agent_start` | user prompt (text, length) |
| `decision` | `message_end` (assistant) | thinking, text/plan, model, stop reason, tokens, cost |
| `tool_call` | `tool_execution_start` | tool name, arguments, call id |
| `tool_result` | `tool_execution_end` | status, duration, error flag, nested usage |
| `model_change` | `model_select` | active model |
| `thinking_change` | `thinking_level_select` | thinking/reasoning level |

Run lifecycle is `run.started` (session start) and `run.completed` (session shutdown).

## Install

```sh
cp integrations/pi/vigil.ts ~/.pi/agent/extensions/vigil.ts
```

Pi loads extensions from `~/.pi/agent/extensions/` on startup. Reload or restart Pi.

## Configuration

The producer resolves its target in this order:

1. process environment (`VIGIL_BASE_URL`, `VIGIL_PROJECT_ID`, `VIGIL_INGEST_KEY`)
2. nearest `.env` walking up from the working directory
3. the global `vigil init` CLI config (`~/Library/Application Support/vigil/config.json` on macOS, `~/.config/vigil/config.json` on Linux)

So with no folder `.env`, every session goes to the global project. A folder with its own `.env` from `vigil init` binds its sessions to that project.

### Switching project for one session

```
/vigil-project                       # list projects and pick one
/vigil-project <project_id> <key>    # switch directly
```

Switching mid-session starts a new run in the new project. Switching before the first prompt is cleanest.

## Configuration variables

Each step carries `trace_id = run_id`, so a Pi session also appears in the Traces view and correlates with app traces when the same project is used.

### Optional variables

| Variable | Default | Meaning |
|---|---|---|
| `VIGIL_AGENT_DISABLED` | `false` | Disable the producer entirely |
| `VIGIL_SOURCE` | `pi` | Run source label |
| `VIGIL_CAPTURE_THINKING` | `true` | Capture model reasoning |
| `VIGIL_CAPTURE_TEXT` | `true` | Capture model text/plans |
| `VIGIL_CAPTURE_TOOL_ARGS` | `true` | Capture tool arguments |
| `VIGIL_CAPTURE_TOOL_OUTPUT` | `false` | Capture truncated tool output |

Sensitive-looking keys (`password`, `token`, `secret`, `authorization`, `cookie`, …)
are redacted client-side, and Vigil redacts again before raw append.

## Querying

```sh
curl 'http://localhost:8080/api/runs?project_id=proj_...'
curl 'http://localhost:8080/api/runs/run_...'
```

The same runs appear in the Vigil UI under the **Runs** tab.
