# Vigil Python SDK

Send logs, traces, and metrics to a running Vigil server from Python.

Published on PyPI as `vigil-observability`.

## Install

From PyPI:

```sh
pip install vigil-observability
```

For OpenTelemetry exporter helpers:

```sh
pip install "vigil-observability[otel]"
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

## OpenTelemetry Helpers

If your app already uses OpenTelemetry, install the optional extra and point the official OTLP/HTTP exporters at Vigil:

```python
from vigil_sdk import configure_vigil_otel

configure_vigil_otel(service_name="my-app")
```

By default this configures tracing, metrics, and Python logging with OTLP/HTTP protobuf exporters for:

- `POST /v1/traces`
- `POST /v1/metrics`
- `POST /v1/logs`

The helper reads `VIGIL_BASE_URL`, `VIGIL_INGEST_KEY`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_HEADERS`, and `OTEL_SERVICE_NAME`. You can also export the standard OpenTelemetry environment values yourself:

```python
import os
from vigil_sdk import build_vigil_otel_env

os.environ.update(build_vigil_otel_env(service_name="my-app"))
```

Equivalent environment:

```env
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:8080
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer%20vigil_...
OTEL_SERVICE_NAME=my-app
```

Native `VigilClient.log`, `trace`, and `metric` stay dependency-free. OpenTelemetry packages are imported only when `configure_vigil_otel` is called.

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
