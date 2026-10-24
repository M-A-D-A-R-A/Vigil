"""Agent run helper: emit decisions, tool calls, and results to Vigil.

Use via :meth:`vigil_sdk.VigilClient.agent_run`:

    with vigil.agent_run("support-agent", trace_id=request_trace_id) as run:
        run.decision(thought="look up the order", model="gpt-4o", tokens=820, cost_usd=0.004)
        run.tool_call("search_orders", {"user_id": 42})
        run.tool_result("search_orders", status="success", duration_ms=18)
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Mapping, Optional


@dataclass
class AgentRun:
    """A single agent invocation that emits ordered run steps to Vigil."""

    client: Any
    run_id: str
    trace_id: Optional[str] = None
    span_id: Optional[str] = None
    _finished: bool = field(default=False, repr=False)

    def _emit(
        self,
        *,
        kind: str,
        name: str,
        step_type: Optional[str] = None,
        span_id: Optional[str] = None,
        attrs: Optional[Mapping[str, Any]] = None,
        body: Any = None,
        level: Optional[str] = None,
    ) -> None:
        self.client.ingest(
            kind=kind,
            name=name,
            run_id=self.run_id,
            step_type=step_type,
            trace_id=self.trace_id,
            span_id=span_id or self.span_id,
            attrs=dict(attrs or {}),
            body=body,
            level=level,
        )

    def decision(
        self,
        *,
        thought: Optional[str] = None,
        text: Optional[str] = None,
        model: Optional[str] = None,
        tokens: Optional[float] = None,
        cost_usd: Optional[float] = None,
        span_id: Optional[str] = None,
        attrs: Optional[Mapping[str, Any]] = None,
    ) -> "AgentRun":
        """Record a model decision (reasoning and/or plan)."""
        merged = dict(attrs or {})
        if model:
            merged["model"] = model
        if tokens is not None:
            merged["total_tokens"] = tokens
        if cost_usd is not None:
            merged["cost_usd"] = cost_usd
        body: dict[str, Any] = {}
        if thought:
            body["thinking"] = thought
        if text:
            body["text"] = text
        self._emit(
            kind="run_step",
            name="assistant.decision",
            step_type="decision",
            span_id=span_id,
            attrs=merged,
            body=body or None,
        )
        return self

    def tool_call(
        self,
        name: str,
        args: Optional[Mapping[str, Any]] = None,
        *,
        span_id: Optional[str] = None,
        attrs: Optional[Mapping[str, Any]] = None,
    ) -> "AgentRun":
        """Record a tool or function call and its arguments."""
        self._emit(
            kind="run_step",
            name=name,
            step_type="tool_call",
            span_id=span_id,
            attrs=dict(attrs or {}),
            body={"args": args} if args is not None else None,
        )
        return self

    def tool_result(
        self,
        name: str,
        *,
        status: str = "success",
        duration_ms: Optional[float] = None,
        tokens: Optional[float] = None,
        cost_usd: Optional[float] = None,
        error: Optional[str] = None,
        output: Optional[str] = None,
        span_id: Optional[str] = None,
        attrs: Optional[Mapping[str, Any]] = None,
    ) -> "AgentRun":
        """Record a tool or function result."""
        merged = dict(attrs or {})
        merged["status"] = status
        if duration_ms is not None:
            merged["duration_ms"] = duration_ms
        if tokens is not None:
            merged["total_tokens"] = tokens
        if cost_usd is not None:
            merged["cost_usd"] = cost_usd
        if error:
            merged["error"] = error
        self._emit(
            kind="run_step",
            name=name,
            step_type="tool_result",
            span_id=span_id,
            attrs=merged,
            body={"message": output} if output is not None else None,
            level="error" if status == "error" else None,
        )
        return self

    def step(
        self,
        step_type: str,
        name: str,
        *,
        attrs: Optional[Mapping[str, Any]] = None,
        body: Any = None,
        span_id: Optional[str] = None,
        level: Optional[str] = None,
    ) -> "AgentRun":
        """Record a custom step type."""
        self._emit(
            kind="run_step",
            name=name,
            step_type=step_type,
            span_id=span_id,
            attrs=dict(attrs or {}),
            body=body,
            level=level,
        )
        return self

    def finish(
        self,
        *,
        status: str = "succeeded",
        outcome: Optional[str] = None,
        failure_class: Optional[str] = None,
    ) -> "AgentRun":
        """Mark the run finished. Safe to call more than once."""
        if self._finished:
            return self
        self._finished = True
        merged: dict[str, Any] = {"status": status}
        if outcome:
            merged["outcome"] = outcome
        if failure_class:
            merged["failure_class"] = failure_class
        self._emit(kind="run", name="run.completed", attrs=merged)
        return self

    @property
    def finished(self) -> bool:
        return self._finished
