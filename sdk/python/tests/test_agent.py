from __future__ import annotations

import json
import unittest

from vigil_sdk import AgentRun, VigilClient


class FakeTransport:
    def __init__(self):
        self.calls = []

    def __call__(self, method, url, headers, body, timeout):
        self.calls.append(json.loads(body.decode("utf-8")))
        return 202, json.dumps(
            {"event_id": "evt_1", "received_at": "2026-05-10T00:00:00Z", "indexed_async": True}
        ).encode("utf-8")


class AgentRunTest(unittest.TestCase):
    def _client(self, transport):
        return VigilClient(
            base_url="http://vigil.local",
            project_id="proj_1",
            ingest_key="vigil_x",
            transport=transport,
        )

    def test_agent_run_emits_correlated_steps(self):
        transport = FakeTransport()
        client = self._client(transport)

        with client.agent_run("support-agent", run_id="run_1", trace_id="trace_1", prompt="help") as run:
            self.assertIsInstance(run, AgentRun)
            run.decision(thought="think", model="gpt-4o", tokens=120, cost_usd=0.003)
            run.tool_call("search", {"q": "x"})
            run.tool_result("search", status="success", duration_ms=12)

        first = transport.calls[0]
        self.assertEqual(first["kind"], "run")
        self.assertEqual(first["name"], "support-agent")
        self.assertEqual(first["attrs"]["status"], "running")
        self.assertEqual(first["attrs"]["prompt"], "help")

        steps = [c for c in transport.calls if c["kind"] == "run_step"]
        self.assertEqual([s["step_type"] for s in steps], ["decision", "tool_call", "tool_result"])
        for step in steps:
            self.assertEqual(step["run_id"], "run_1")
            self.assertEqual(step["trace_id"], "trace_1")

        last = transport.calls[-1]
        self.assertEqual(last["kind"], "run")
        self.assertEqual(last["attrs"]["status"], "succeeded")

    def test_agent_run_marks_failure(self):
        transport = FakeTransport()
        client = self._client(transport)

        with self.assertRaises(ValueError):
            with client.agent_run("agent", run_id="run_2") as run:
                run.decision(thought="oops")
                raise ValueError("boom")

        last = transport.calls[-1]
        self.assertEqual(last["attrs"]["status"], "failed")
        self.assertEqual(last["attrs"]["failure_class"], "ValueError")

    def test_agent_run_finish_is_idempotent(self):
        transport = FakeTransport()
        client = self._client(transport)

        with client.agent_run("agent", run_id="run_3") as run:
            run.finish(status="canceled")

        finals = [c for c in transport.calls if c["kind"] == "run" and c["attrs"].get("status") == "canceled"]
        self.assertEqual(len(finals), 1)
        self.assertFalse(any(c["attrs"].get("status") == "succeeded" for c in transport.calls))


if __name__ == "__main__":
    unittest.main()
