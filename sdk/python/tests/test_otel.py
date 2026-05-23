from __future__ import annotations

import logging
import os
import sys
import types
import unittest
from unittest.mock import patch

from vigil_sdk import VigilConfigError, build_vigil_otel_env, configure_vigil_otel


class VigilOTelTest(unittest.TestCase):
    def test_build_vigil_otel_env_from_vigil_values(self):
        with patch.dict(
            os.environ,
            {
                "VIGIL_BASE_URL": "http://localhost:8080/",
                "VIGIL_INGEST_KEY": "vigil_123",
                "OTEL_SERVICE_NAME": "checkout-api",
            },
            clear=True,
        ):
            env = build_vigil_otel_env()

        self.assertEqual(env["OTEL_EXPORTER_OTLP_ENDPOINT"], "http://localhost:8080")
        self.assertEqual(env["OTEL_EXPORTER_OTLP_PROTOCOL"], "http/protobuf")
        self.assertEqual(env["OTEL_EXPORTER_OTLP_HEADERS"], "Authorization=Bearer%20vigil_123")
        self.assertEqual(env["OTEL_SERVICE_NAME"], "checkout-api")

    def test_configure_requires_ingest_key_or_authorization_header(self):
        with patch.dict(os.environ, {"VIGIL_BASE_URL": "http://localhost:8080"}, clear=True):
            with self.assertRaisesRegex(
                VigilConfigError,
                "VIGIL_INGEST_KEY or OTEL_EXPORTER_OTLP_HEADERS Authorization is required",
            ):
                configure_vigil_otel(traces=False, metrics=False, logs=False)

    def test_configure_wires_trace_metric_and_log_exporters(self):
        state, modules = fake_otel_modules()
        with patch.dict(sys.modules, modules), patch.dict(
            os.environ,
            {
                "VIGIL_BASE_URL": "http://localhost:8080/",
                "VIGIL_INGEST_KEY": "vigil_123",
                "OTEL_SERVICE_NAME": "worker",
            },
            clear=True,
        ):
            config = configure_vigil_otel(attach_logging_handler=False)

        self.assertEqual(config.endpoint, "http://localhost:8080")
        self.assertEqual(config.traces_endpoint, "http://localhost:8080/v1/traces")
        self.assertEqual(config.metrics_endpoint, "http://localhost:8080/v1/metrics")
        self.assertEqual(config.logs_endpoint, "http://localhost:8080/v1/logs")
        self.assertEqual(config.headers, {"Authorization": "Bearer vigil_123"})
        self.assertEqual(config.service_name, "worker")
        self.assertEqual(config.resource, {"service.name": "worker"})
        self.assertIs(state["tracer_provider"], config.tracer_provider)
        self.assertIs(state["meter_provider"], config.meter_provider)
        self.assertIs(state["logger_provider"], config.logger_provider)
        self.assertEqual(
            config.tracer_provider.span_processors[0].exporter.kwargs["endpoint"],
            "http://localhost:8080/v1/traces",
        )
        self.assertEqual(
            config.meter_provider.metric_readers[0].exporter.kwargs["endpoint"],
            "http://localhost:8080/v1/metrics",
        )
        self.assertEqual(
            config.logger_provider.log_record_processors[0].exporter.kwargs["endpoint"],
            "http://localhost:8080/v1/logs",
        )


def fake_otel_modules():
    state = {}
    modules = {
        "opentelemetry": types.ModuleType("opentelemetry"),
        "opentelemetry._logs": types.ModuleType("opentelemetry._logs"),
        "opentelemetry.metrics": types.ModuleType("opentelemetry.metrics"),
        "opentelemetry.trace": types.ModuleType("opentelemetry.trace"),
        "opentelemetry.exporter": types.ModuleType("opentelemetry.exporter"),
        "opentelemetry.exporter.otlp": types.ModuleType("opentelemetry.exporter.otlp"),
        "opentelemetry.exporter.otlp.proto": types.ModuleType("opentelemetry.exporter.otlp.proto"),
        "opentelemetry.exporter.otlp.proto.http": types.ModuleType(
            "opentelemetry.exporter.otlp.proto.http"
        ),
        "opentelemetry.exporter.otlp.proto.http._log_exporter": types.ModuleType(
            "opentelemetry.exporter.otlp.proto.http._log_exporter"
        ),
        "opentelemetry.exporter.otlp.proto.http.metric_exporter": types.ModuleType(
            "opentelemetry.exporter.otlp.proto.http.metric_exporter"
        ),
        "opentelemetry.exporter.otlp.proto.http.trace_exporter": types.ModuleType(
            "opentelemetry.exporter.otlp.proto.http.trace_exporter"
        ),
        "opentelemetry.sdk": types.ModuleType("opentelemetry.sdk"),
        "opentelemetry.sdk._logs": types.ModuleType("opentelemetry.sdk._logs"),
        "opentelemetry.sdk._logs.export": types.ModuleType("opentelemetry.sdk._logs.export"),
        "opentelemetry.sdk.metrics": types.ModuleType("opentelemetry.sdk.metrics"),
        "opentelemetry.sdk.metrics.export": types.ModuleType("opentelemetry.sdk.metrics.export"),
        "opentelemetry.sdk.resources": types.ModuleType("opentelemetry.sdk.resources"),
        "opentelemetry.sdk.trace": types.ModuleType("opentelemetry.sdk.trace"),
        "opentelemetry.sdk.trace.export": types.ModuleType("opentelemetry.sdk.trace.export"),
    }

    modules["opentelemetry"]._logs = modules["opentelemetry._logs"]
    modules["opentelemetry"].metrics = modules["opentelemetry.metrics"]
    modules["opentelemetry"].trace = modules["opentelemetry.trace"]

    modules["opentelemetry._logs"].set_logger_provider = (
        lambda provider: state.__setitem__("logger_provider", provider)
    )
    modules["opentelemetry.metrics"].set_meter_provider = (
        lambda provider: state.__setitem__("meter_provider", provider)
    )
    modules["opentelemetry.trace"].set_tracer_provider = (
        lambda provider: state.__setitem__("tracer_provider", provider)
    )

    modules["opentelemetry.sdk.resources"].Resource = FakeResource
    modules["opentelemetry.sdk.trace"].TracerProvider = FakeTracerProvider
    modules["opentelemetry.sdk.trace.export"].BatchSpanProcessor = FakeSpanProcessor
    modules["opentelemetry.sdk.metrics"].MeterProvider = FakeMeterProvider
    modules["opentelemetry.sdk.metrics.export"].PeriodicExportingMetricReader = FakeMetricReader
    modules["opentelemetry.sdk._logs"].LoggerProvider = FakeLoggerProvider
    modules["opentelemetry.sdk._logs"].LoggingHandler = FakeLoggingHandler
    modules["opentelemetry.sdk._logs.export"].BatchLogRecordProcessor = FakeLogRecordProcessor
    modules["opentelemetry.exporter.otlp.proto.http.trace_exporter"].OTLPSpanExporter = FakeExporter
    modules["opentelemetry.exporter.otlp.proto.http.metric_exporter"].OTLPMetricExporter = FakeExporter
    modules["opentelemetry.exporter.otlp.proto.http._log_exporter"].OTLPLogExporter = FakeExporter

    return state, modules


class FakeResource:
    @classmethod
    def create(cls, attrs):
        return dict(attrs)


class FakeExporter:
    def __init__(self, **kwargs):
        self.kwargs = kwargs


class FakeSpanProcessor:
    def __init__(self, exporter):
        self.exporter = exporter


class FakeLogRecordProcessor:
    def __init__(self, exporter):
        self.exporter = exporter


class FakeMetricReader:
    def __init__(self, exporter, export_interval_millis):
        self.exporter = exporter
        self.export_interval_millis = export_interval_millis


class FakeTracerProvider:
    def __init__(self, *, resource):
        self.resource = resource
        self.span_processors = []

    def add_span_processor(self, processor):
        self.span_processors.append(processor)


class FakeMeterProvider:
    def __init__(self, *, resource, metric_readers):
        self.resource = resource
        self.metric_readers = metric_readers


class FakeLoggerProvider:
    def __init__(self, *, resource):
        self.resource = resource
        self.log_record_processors = []

    def add_log_record_processor(self, processor):
        self.log_record_processors.append(processor)


class FakeLoggingHandler(logging.Handler):
    def __init__(self, *, level, logger_provider):
        super().__init__(level)
        self.logger_provider = logger_provider


if __name__ == "__main__":
    unittest.main()
