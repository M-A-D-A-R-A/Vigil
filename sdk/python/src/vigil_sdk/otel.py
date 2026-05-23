from __future__ import annotations

from dataclasses import dataclass
import logging
import os
from typing import Any, Mapping, Optional
from urllib.parse import quote, unquote

from .client import VigilConfigError


class VigilOTelError(VigilConfigError):
    """Raised when OpenTelemetry helper configuration fails."""


@dataclass(frozen=True)
class VigilOTelConfig:
    """Objects and values created by configure_vigil_otel."""

    endpoint: str
    traces_endpoint: Optional[str]
    metrics_endpoint: Optional[str]
    logs_endpoint: Optional[str]
    headers: dict[str, str]
    service_name: str
    resource: Any
    tracer_provider: Any = None
    meter_provider: Any = None
    logger_provider: Any = None
    logging_handler: Any = None

    def force_flush(self, timeout_millis: int = 30000) -> None:
        for provider in (self.tracer_provider, self.meter_provider, self.logger_provider):
            if provider is not None and hasattr(provider, "force_flush"):
                provider.force_flush(timeout_millis=timeout_millis)

    def shutdown(self, timeout_millis: int = 30000) -> None:
        for provider in (self.tracer_provider, self.meter_provider, self.logger_provider):
            if provider is not None and hasattr(provider, "shutdown"):
                try:
                    provider.shutdown(timeout_millis=timeout_millis)
                except TypeError:
                    provider.shutdown()


def configure_vigil_otel(
    *,
    endpoint: Optional[str] = None,
    traces_endpoint: Optional[str] = None,
    metrics_endpoint: Optional[str] = None,
    logs_endpoint: Optional[str] = None,
    ingest_key: Optional[str] = None,
    service_name: Optional[str] = None,
    resource_attributes: Optional[Mapping[str, Any]] = None,
    headers: Optional[Mapping[str, str]] = None,
    timeout: Optional[float] = None,
    traces: bool = True,
    metrics: bool = True,
    logs: bool = True,
    attach_logging_handler: bool = True,
    logger_name: str = "",
    log_level: int = logging.INFO,
    metric_export_interval_millis: int = 60000,
    set_global: bool = True,
    set_env: bool = False,
) -> VigilOTelConfig:
    """Configure official OpenTelemetry OTLP/HTTP exporters for Vigil.

    The normal Vigil SDK stays stdlib-only. This helper imports OpenTelemetry
    only when called and is intended for installs with:

        pip install "vigil-observability[otel]"
    """

    resolved_endpoint = _resolve_endpoint(endpoint)
    resolved_service_name = _resolve_service_name(service_name)
    resolved_headers = _resolve_headers(ingest_key=ingest_key, headers=headers)

    if set_env:
        os.environ.update(
            build_vigil_otel_env(
                endpoint=resolved_endpoint,
                ingest_key=None,
                service_name=resolved_service_name,
                headers=resolved_headers,
            )
        )

    resolved_traces_endpoint = _resolve_signal_endpoint(
        traces_endpoint,
        "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
        resolved_endpoint,
        "/v1/traces",
    )
    resolved_metrics_endpoint = _resolve_signal_endpoint(
        metrics_endpoint,
        "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
        resolved_endpoint,
        "/v1/metrics",
    )
    resolved_logs_endpoint = _resolve_signal_endpoint(
        logs_endpoint,
        "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
        resolved_endpoint,
        "/v1/logs",
    )

    imports = _load_otel_imports()
    resource = imports["Resource"].create(
        {
            "service.name": resolved_service_name,
            **dict(resource_attributes or {}),
        }
    )

    tracer_provider = None
    meter_provider = None
    logger_provider = None
    logging_handler = None

    if traces:
        span_exporter = imports["OTLPSpanExporter"](
            endpoint=resolved_traces_endpoint,
            headers=resolved_headers,
            timeout=timeout,
        )
        tracer_provider = imports["TracerProvider"](resource=resource)
        tracer_provider.add_span_processor(imports["BatchSpanProcessor"](span_exporter))
        if set_global:
            imports["trace"].set_tracer_provider(tracer_provider)

    if metrics:
        metric_exporter = imports["OTLPMetricExporter"](
            endpoint=resolved_metrics_endpoint,
            headers=resolved_headers,
            timeout=timeout,
        )
        reader = imports["PeriodicExportingMetricReader"](
            metric_exporter,
            export_interval_millis=metric_export_interval_millis,
        )
        meter_provider = imports["MeterProvider"](resource=resource, metric_readers=[reader])
        if set_global:
            imports["metrics"].set_meter_provider(meter_provider)

    if logs:
        log_exporter = imports["OTLPLogExporter"](
            endpoint=resolved_logs_endpoint,
            headers=resolved_headers,
            timeout=timeout,
        )
        logger_provider = imports["LoggerProvider"](resource=resource)
        logger_provider.add_log_record_processor(imports["BatchLogRecordProcessor"](log_exporter))
        if set_global:
            imports["_logs"].set_logger_provider(logger_provider)
        if attach_logging_handler:
            logging_handler = imports["LoggingHandler"](
                level=log_level,
                logger_provider=logger_provider,
            )
            logging.getLogger(logger_name).addHandler(logging_handler)

    return VigilOTelConfig(
        endpoint=resolved_endpoint,
        traces_endpoint=resolved_traces_endpoint if traces else None,
        metrics_endpoint=resolved_metrics_endpoint if metrics else None,
        logs_endpoint=resolved_logs_endpoint if logs else None,
        headers=resolved_headers,
        service_name=resolved_service_name,
        resource=resource,
        tracer_provider=tracer_provider,
        meter_provider=meter_provider,
        logger_provider=logger_provider,
        logging_handler=logging_handler,
    )


def build_vigil_otel_env(
    *,
    endpoint: Optional[str] = None,
    ingest_key: Optional[str] = None,
    service_name: Optional[str] = None,
    headers: Optional[Mapping[str, str]] = None,
) -> dict[str, str]:
    """Return standard OTEL_* environment values for exporting to Vigil."""

    resolved_endpoint = _resolve_endpoint(endpoint)
    resolved_headers = _resolve_headers(ingest_key=ingest_key, headers=headers)
    return {
        "OTEL_EXPORTER_OTLP_ENDPOINT": resolved_endpoint,
        "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
        "OTEL_EXPORTER_OTLP_HEADERS": _format_env_headers(resolved_headers),
        "OTEL_SERVICE_NAME": _resolve_service_name(service_name),
    }


def _load_otel_imports() -> dict[str, Any]:
    try:
        from opentelemetry import _logs, metrics, trace
        from opentelemetry.exporter.otlp.proto.http._log_exporter import OTLPLogExporter
        from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter
        from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
        from opentelemetry.sdk._logs import LoggerProvider, LoggingHandler
        from opentelemetry.sdk._logs.export import BatchLogRecordProcessor
        from opentelemetry.sdk.metrics import MeterProvider
        from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
        from opentelemetry.sdk.resources import Resource
        from opentelemetry.sdk.trace import TracerProvider
        from opentelemetry.sdk.trace.export import BatchSpanProcessor
    except ImportError as exc:
        raise VigilOTelError(
            'OpenTelemetry dependencies are not installed. Install with: pip install "vigil-observability[otel]"'
        ) from exc

    return {
        "_logs": _logs,
        "metrics": metrics,
        "trace": trace,
        "OTLPLogExporter": OTLPLogExporter,
        "OTLPMetricExporter": OTLPMetricExporter,
        "OTLPSpanExporter": OTLPSpanExporter,
        "LoggerProvider": LoggerProvider,
        "LoggingHandler": LoggingHandler,
        "BatchLogRecordProcessor": BatchLogRecordProcessor,
        "MeterProvider": MeterProvider,
        "PeriodicExportingMetricReader": PeriodicExportingMetricReader,
        "Resource": Resource,
        "TracerProvider": TracerProvider,
        "BatchSpanProcessor": BatchSpanProcessor,
    }


def _resolve_endpoint(endpoint: Optional[str]) -> str:
    value = _clean(endpoint) or _clean(os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
    value = value or _clean(os.getenv("VIGIL_BASE_URL")) or "http://localhost:8080"
    value = value.rstrip("/")
    if not value.startswith(("http://", "https://")):
        raise VigilConfigError("OTLP endpoint must start with http:// or https://")
    return value


def _resolve_signal_endpoint(
    explicit: Optional[str],
    env_name: str,
    base_endpoint: str,
    signal_path: str,
) -> str:
    value = _clean(explicit) or _clean(os.getenv(env_name))
    if value:
        return value.rstrip("/")
    return base_endpoint.rstrip("/") + signal_path


def _resolve_service_name(service_name: Optional[str]) -> str:
    return (
        _clean(service_name)
        or _clean(os.getenv("OTEL_SERVICE_NAME"))
        or _clean(os.getenv("VIGIL_SOURCE"))
        or "python-sdk"
    )


def _resolve_headers(
    *,
    ingest_key: Optional[str],
    headers: Optional[Mapping[str, str]],
) -> dict[str, str]:
    resolved: dict[str, str] = {}
    resolved.update(_parse_env_headers(os.getenv("OTEL_EXPORTER_OTLP_HEADERS")))
    if headers:
        resolved.update({str(key): str(value) for key, value in headers.items()})

    resolved_ingest_key = _clean(ingest_key) or _clean(os.getenv("VIGIL_INGEST_KEY"))
    if resolved_ingest_key and not _has_header(resolved, "Authorization"):
        resolved["Authorization"] = f"Bearer {resolved_ingest_key}"

    if not _has_header(resolved, "Authorization"):
        raise VigilConfigError("VIGIL_INGEST_KEY or OTEL_EXPORTER_OTLP_HEADERS Authorization is required")
    return resolved


def _parse_env_headers(raw: Optional[str]) -> dict[str, str]:
    headers: dict[str, str] = {}
    if not raw:
        return headers
    for part in raw.split(","):
        if not part.strip() or "=" not in part:
            continue
        key, value = part.split("=", 1)
        cleaned_key = key.strip()
        if cleaned_key:
            headers[cleaned_key] = unquote(value.strip())
    return headers


def _format_env_headers(headers: Mapping[str, str]) -> str:
    return ",".join(f"{key}={quote(value, safe='')}" for key, value in headers.items())


def _has_header(headers: Mapping[str, str], name: str) -> bool:
    return any(key.lower() == name.lower() for key in headers)


def _clean(value: Optional[str]) -> str:
    return str(value).strip() if value is not None else ""
