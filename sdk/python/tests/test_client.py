from __future__ import annotations

from datetime import datetime, timezone
import json
import os
import unittest
from unittest.mock import patch

from vigil_sdk import (
    NoopVigilClient,
    ProjectResult,
    VigilClient,
    VigilConfigError,
    VigilHTTPError,
    child_trace_context,
    continue_trace_context,
    create_trace_context,
    format_traceparent,
    parse_traceparent,
    traceparent_headers,
)


class FakeTransport:
    def __init__(self, status=202, payload=None):
        self.status = status
        self.payload = payload or {
            "event_id": "evt_123",
            "received_at": "2026-05-10T00:00:00Z",
            "indexed_async": True,
        }
        self.calls = []

    def __call__(self, method, url, headers, body, timeout):
        self.calls.append(
            {
                "method": method,
                "url": url,
                "headers": dict(headers),
                "body": body,
                "timeout": timeout,
            }
        )
        return self.status, json.dumps(self.payload).encode("utf-8")


class VigilClientTest(unittest.TestCase):
    def test_from_env_reads_vigil_init_values(self):
        transport = FakeTransport()
        with patch.dict(
            os.environ,
            {
                "VIGIL_BASE_URL": "http://localhost:8080/",
                "VIGIL_PROJECT_ID": "proj_123",
                "VIGIL_INGEST_KEY": "vigil_123",
            },
            clear=True,
        ):
            client = VigilClient.from_env(transport=transport)

        self.assertEqual(client.base_url, "http://localhost:8080")
        self.assertEqual(client.project_id, "proj_123")
        self.assertEqual(client.ingest_key, "vigil_123")

    def test_from_env_requires_project_and_key(self):
        with patch.dict(os.environ, {"VIGIL_BASE_URL": "http://localhost:8080"}, clear=True):
            with self.assertRaisesRegex(VigilConfigError, "VIGIL_PROJECT_ID, VIGIL_INGEST_KEY"):
                VigilClient.from_env(transport=FakeTransport())

    def test_from_env_optional_returns_noop_client(self):
        with patch.dict(os.environ, {"VIGIL_BASE_URL": "http://localhost:8080"}, clear=True):
            client = VigilClient.from_env(optional=True, transport=FakeTransport())

        self.assertIsInstance(client, NoopVigilClient)
        result = client.log("request.completed", message="ok")
        self.assertEqual(result.event_id, "")
        self.assertFalse(result.indexed_async)
        self.assertEqual(client.logs()["total"], 0)
        self.assertEqual(client.health()["status"], "disabled")

    def test_log_sends_ingest_envelope(self):
        transport = FakeTransport()
        client = VigilClient(
            base_url="http://vigil.local",
            project_id="proj_123",
            ingest_key="vigil_123",
            transport=transport,
        )

        result = client.log(
            "request.completed",
            message="ok",
            attrs={"route": "/health"},
            ts=datetime(2026, 5, 10, 1, 2, 3, tzinfo=timezone.utc),
        )

        self.assertEqual(result.event_id, "evt_123")
        call = transport.calls[0]
        self.assertEqual(call["method"], "POST")
        self.assertEqual(call["url"], "http://vigil.local/api/ingest")
        self.assertEqual(call["headers"]["Authorization"], "Bearer vigil_123")
        envelope = json.loads(call["body"].decode("utf-8"))
        self.assertEqual(
            envelope,
            {
                "schema_version": 1,
                "project_id": "proj_123",
                "kind": "log",
                "ts": "2026-05-10T01:02:03Z",
                "source": "python-sdk",
                "name": "request.completed",
                "attrs": {"route": "/health"},
                "body": {"message": "ok"},
                "level": "info",
            },
        )

    def test_trace_sends_trace_fields(self):
        transport = FakeTransport()
        client = VigilClient(project_id="proj_123", ingest_key="vigil_123", transport=transport)

        client.trace("llm.completed", trace_id="trace-1", span_id="span-1", parent_span_id="span-root")

        envelope = json.loads(transport.calls[0]["body"].decode("utf-8"))
        self.assertEqual(envelope["kind"], "trace")
        self.assertEqual(envelope["trace_id"], "trace-1")
        self.assertEqual(envelope["span_id"], "span-1")
        self.assertEqual(envelope["parent_span_id"], "span-root")

    def test_traceparent_helpers(self):
        parent = parse_traceparent("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

        self.assertIsNotNone(parent)
        assert parent is not None
        self.assertEqual(parent.trace_id, "4bf92f3577b34da6a3ce929d0e0e4736")
        self.assertEqual(parent.span_id, "00f067aa0ba902b7")
        self.assertTrue(parent.sampled)
        self.assertEqual(
            format_traceparent(parent),
            "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
        )
        self.assertEqual(
            traceparent_headers(parent),
            {"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
        )

        child = child_trace_context(parent)
        self.assertEqual(child.trace_id, parent.trace_id)
        self.assertEqual(child.parent_span_id, parent.span_id)
        self.assertRegex(child.span_id, r"^[0-9a-f]{16}$")

        continued = continue_trace_context("00-4bf92f3577b34da6a3ce929d0e0e4736-1111111111111111-00")
        self.assertEqual(continued.trace_id, "4bf92f3577b34da6a3ce929d0e0e4736")
        self.assertEqual(continued.parent_span_id, "1111111111111111")
        self.assertIsNone(parse_traceparent("00-00000000000000000000000000000000-00f067aa0ba902b7-01"))

        fresh = create_trace_context()
        self.assertRegex(fresh.trace_id, r"^[0-9a-f]{32}$")
        self.assertRegex(fresh.span_id, r"^[0-9a-f]{16}$")

    def test_metric_puts_value_and_unit_in_attrs(self):
        transport = FakeTransport()
        client = VigilClient(project_id="proj_123", ingest_key="vigil_123", transport=transport)

        client.metric("queue.depth", value=7, unit="count", attrs={"queue": "jobs"})

        envelope = json.loads(transport.calls[0]["body"].decode("utf-8"))
        self.assertEqual(envelope["kind"], "metric")
        self.assertEqual(envelope["attrs"], {"queue": "jobs", "value": 7, "unit": "count"})

    def test_create_project_returns_project_result(self):
        transport = FakeTransport(
            status=201,
            payload={"project": {"id": "proj_123", "name": "demo"}, "ingest_key": "vigil_123"},
        )
        client = VigilClient(transport=transport)

        result = client.create_project("demo")

        self.assertIsInstance(result, ProjectResult)
        self.assertEqual(result.project["id"], "proj_123")
        self.assertEqual(result.ingest_key, "vigil_123")
        body = json.loads(transport.calls[0]["body"].decode("utf-8"))
        self.assertEqual(body, {"name": "demo"})

    def test_query_defaults_to_client_project_id(self):
        transport = FakeTransport(status=200, payload={"items": []})
        client = VigilClient(project_id="proj_123", ingest_key="vigil_123", transport=transport)

        client.logs(limit=10)

        self.assertEqual(transport.calls[0]["method"], "GET")
        self.assertIn("/api/logs?", transport.calls[0]["url"])
        self.assertIn("project_id=proj_123", transport.calls[0]["url"])
        self.assertIn("limit=10", transport.calls[0]["url"])

    def test_http_errors_include_server_message(self):
        transport = FakeTransport(status=401, payload={"error": "authorization header is required"})
        client = VigilClient(project_id="proj_123", ingest_key="vigil_123", transport=transport)

        with self.assertRaises(VigilHTTPError) as raised:
            client.log("request.completed")

        self.assertEqual(raised.exception.status_code, 401)
        self.assertIn("authorization header is required", str(raised.exception))


if __name__ == "__main__":
    unittest.main()
