# Vigil Python SDK

Send logs, traces, and metrics to a running Vigil server from Python.

Published on PyPI as `vigil-observability`.

## Install

From PyPI:

```sh
pip install vigil-observability
```

Import it as `vigil_sdk`:

```python
from vigil_sdk import VigilClient
```

From this repository during development:

```sh
pip install -e sdk/python
```

## Configure

Run `vigil init` in your app directory first. It writes the SDK environment values to `.env`:

```env
VIGIL_BASE_URL=http://localhost:8080
VIGIL_PROJECT_ID=proj_...
VIGIL_INGEST_KEY=vigil_...
```

Load those values into your process, then create a client:

```python
from vigil_sdk import VigilClient

vigil = VigilClient.from_env()
```

For optional instrumentation, use `optional=True`. This returns a no-op client when `VIGIL_PROJECT_ID` or `VIGIL_INGEST_KEY` is missing, so app code can keep calling `log`, `trace`, and `metric` without scattered configuration checks:

```python
vigil = VigilClient.from_env(optional=True)
vigil.log("app.started", message="app started")
```

## Send Events

```python
vigil.log("request.completed", message="request completed", attrs={"route": "/health"})

vigil.trace(
    "llm.completed",
    trace_id="trace-123",
    span_id="span-1",
    attrs={"total_tokens": 42, "cost_usd": 0.0021},
)

vigil.metric("queue.depth", value=7, unit="count", attrs={"queue": "jobs"})
```

Use `VigilClient(base_url=..., project_id=..., ingest_key=...)` if you do not want to configure through environment variables.

## Publish

Use a virtual environment for publishing tools. This avoids `externally-managed-environment` errors on Homebrew and other managed Python installs.

```sh
cd sdk/python
python3 -m venv .venv
source .venv/bin/activate
python -m pip install --upgrade pip build twine
```

Build and check the package:

```sh
python -m build
python -m twine check dist/*
```

Publish to PyPI:

```sh
python -m twine upload dist/*
```

The first release is available at <https://pypi.org/project/vigil-observability/0.1.0/>.
