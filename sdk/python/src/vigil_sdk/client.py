from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timezone
import json
import os
from typing import Any, Callable, Mapping, Optional, Union
from urllib.error import HTTPError
from urllib.parse import quote, urlencode
from urllib.request import Request, urlopen

JSON = Mapping[str, Any]
Transport = Callable[[str, str, Mapping[str, str], Optional[bytes], float], tuple[int, bytes]]


class VigilError(Exception):
    """Base SDK error."""


class VigilConfigError(VigilError):
    """Raised when required SDK configuration is missing."""


class VigilHTTPError(VigilError):
    """Raised when Vigil returns a non-2xx HTTP response."""

    def __init__(self, status_code: int, message: str, body: str = "") -> None:
        super().__init__(message)
        self.status_code = status_code
        self.body = body


@dataclass(frozen=True)
class IngestResult:
    event_id: str
    received_at: str
    indexed_async: bool

    @classmethod
    def from_json(cls, payload: JSON) -> "IngestResult":
        return cls(
            event_id=str(payload.get("event_id", "")),
            received_at=str(payload.get("received_at", "")),
            indexed_async=bool(payload.get("indexed_async", False)),
        )


@dataclass(frozen=True)
class ProjectResult:
    project: dict[str, Any]
    ingest_key: str

    @classmethod
    def from_json(cls, payload: JSON) -> "ProjectResult":
        return cls(
            project=dict(payload.get("project") or {}),
            ingest_key=str(payload.get("ingest_key", "")),
        )


class VigilClient:
    """Small stdlib-only client for the Vigil HTTP API."""

    def __init__(
        self,
        *,
        base_url: str = "http://localhost:8080",
        project_id: Optional[str] = None,
        ingest_key: Optional[str] = None,
        source: str = "python-sdk",
        timeout: float = 10.0,
        transport: Optional[Transport] = None,
    ) -> None:
        self.base_url = _normalize_base_url(base_url)
        self.project_id = _clean(project_id)
        self.ingest_key = _clean(ingest_key)
        self.source = _clean(source) or "python-sdk"
        self.timeout = timeout
        self._transport = transport or _urlopen_transport

    @classmethod
    def from_env(
        cls,
        *,
        source: Optional[str] = None,
        timeout: float = 10.0,
        transport: Optional[Transport] = None,
    ) -> "VigilClient":
        base_url = os.getenv("VIGIL_BASE_URL", "http://localhost:8080")
        project_id = os.getenv("VIGIL_PROJECT_ID")
        ingest_key = os.getenv("VIGIL_INGEST_KEY")
        resolved_source = source or os.getenv("VIGIL_SOURCE", "python-sdk")

        missing = [
            key
            for key, value in {
                "VIGIL_PROJECT_ID": project_id,
                "VIGIL_INGEST_KEY": ingest_key,
            }.items()
            if not _clean(value)
        ]
        if missing:
            raise VigilConfigError(f"missing required environment variables: {', '.join(missing)}")

        return cls(
            base_url=base_url,
            project_id=project_id,
            ingest_key=ingest_key,
            source=resolved_source,
            timeout=timeout,
            transport=transport,
        )

    def health(self) -> dict[str, Any]:
        return self._request("GET", "/api/health")

    def create_project(self, name: str) -> ProjectResult:
        payload = self._request("POST", "/api/projects", json_body={"name": name})
        return ProjectResult.from_json(payload)

    def list_projects(self) -> list[dict[str, Any]]:
        payload = self._request("GET", "/api/projects")
        projects = payload.get("projects") or []
        return [dict(project) for project in projects]

    def rotate_key(self, project_id: Optional[str] = None) -> ProjectResult:
        resolved_project_id = _clean(project_id) or self.project_id
        if not resolved_project_id:
            raise VigilConfigError("project_id is required to rotate an ingest key")
        payload = self._request("POST", f"/api/projects/{quote(resolved_project_id)}/keys/regenerate")
        return ProjectResult.from_json(payload)

    def ingest(
        self,
        *,
        kind: str,
        name: str,
        attrs: Optional[Mapping[str, Any]] = None,
        body: Any = None,
        ts: Optional[Union[datetime, str]] = None,
        source: Optional[str] = None,
        level: Optional[str] = None,
        trace_id: Optional[str] = None,
        span_id: Optional[str] = None,
    ) -> IngestResult:
        self._require_ingest_config()

        envelope: dict[str, Any] = {
            "schema_version": 1,
            "project_id": self.project_id,
            "kind": _required("kind", kind).lower(),
            "ts": _format_ts(ts),
            "source": _clean(source) or self.source,
            "name": _required("name", name),
            "attrs": dict(attrs or {}),
            "body": body,
        }
        _set_if_present(envelope, "level", level)
        _set_if_present(envelope, "trace_id", trace_id)
        _set_if_present(envelope, "span_id", span_id)

        payload = self._request(
            "POST",
            "/api/ingest",
            headers={"Authorization": f"Bearer {self.ingest_key}"},
            json_body=envelope,
        )
        return IngestResult.from_json(payload)

    def log(
        self,
        name: str,
        *,
        message: Optional[str] = None,
        level: str = "info",
        attrs: Optional[Mapping[str, Any]] = None,
        body: Any = None,
        ts: Optional[Union[datetime, str]] = None,
        source: Optional[str] = None,
    ) -> IngestResult:
        if body is None and message is not None:
            body = {"message": message}
        return self.ingest(
            kind="log",
            name=name,
            level=level,
            attrs=attrs,
            body=body,
            ts=ts,
            source=source,
        )

    def trace(
        self,
        name: str,
        *,
        trace_id: str,
        span_id: Optional[str] = None,
        level: str = "info",
        attrs: Optional[Mapping[str, Any]] = None,
        body: Any = None,
        ts: Optional[Union[datetime, str]] = None,
        source: Optional[str] = None,
    ) -> IngestResult:
        return self.ingest(
            kind="trace",
            name=name,
            level=level,
            trace_id=trace_id,
            span_id=span_id,
            attrs=attrs,
            body=body,
            ts=ts,
            source=source,
        )

    def metric(
        self,
        name: str,
        *,
        value: Union[int, float],
        unit: Optional[str] = None,
        attrs: Optional[Mapping[str, Any]] = None,
        body: Any = None,
        ts: Optional[Union[datetime, str]] = None,
        source: Optional[str] = None,
    ) -> IngestResult:
        metric_attrs = dict(attrs or {})
        metric_attrs["value"] = value
        if unit:
            metric_attrs["unit"] = unit
        return self.ingest(
            kind="metric",
            name=name,
            attrs=metric_attrs,
            body=body,
            ts=ts,
            source=source,
        )

    def logs(self, **params: Any) -> dict[str, Any]:
        return self._query("/api/logs", params)

    def traces(self, **params: Any) -> dict[str, Any]:
        return self._query("/api/traces", params)

    def stats(self, **params: Any) -> dict[str, Any]:
        return self._query("/api/stats", params)

    def _query(self, path: str, params: Mapping[str, Any]) -> dict[str, Any]:
        query = {key: value for key, value in params.items() if value is not None}
        if "project_id" not in query and self.project_id:
            query["project_id"] = self.project_id
        suffix = f"?{urlencode(query)}" if query else ""
        return self._request("GET", f"{path}{suffix}")

    def _request(
        self,
        method: str,
        path: str,
        *,
        headers: Optional[Mapping[str, str]] = None,
        json_body: Any = None,
    ) -> dict[str, Any]:
        request_headers = {"Accept": "application/json"}
        if headers:
            request_headers.update(headers)

        body: Optional[bytes] = None
        if json_body is not None:
            body = json.dumps(json_body, separators=(",", ":"), default=_json_default).encode("utf-8")
            request_headers["Content-Type"] = "application/json"

        status_code, response_body = self._transport(
            method,
            self.base_url + path,
            request_headers,
            body,
            self.timeout,
        )
        text = response_body.decode("utf-8")
        if status_code < 200 or status_code >= 300:
            raise VigilHTTPError(status_code, _error_message(status_code, text), text)
        if not text:
            return {}
        return json.loads(text)

    def _require_ingest_config(self) -> None:
        missing = []
        if not self.project_id:
            missing.append("project_id")
        if not self.ingest_key:
            missing.append("ingest_key")
        if missing:
            raise VigilConfigError(f"missing required ingest configuration: {', '.join(missing)}")


def _urlopen_transport(
    method: str,
    url: str,
    headers: Mapping[str, str],
    body: Optional[bytes],
    timeout: float,
) -> tuple[int, bytes]:
    request = Request(url, data=body, headers=dict(headers), method=method)
    try:
        with urlopen(request, timeout=timeout) as response:
            return response.status, response.read()
    except HTTPError as exc:
        return exc.code, exc.read()


def _normalize_base_url(raw: str) -> str:
    value = _required("base_url", raw).rstrip("/")
    if not value.startswith(("http://", "https://")):
        raise VigilConfigError("base_url must start with http:// or https://")
    return value


def _format_ts(value: Optional[Union[datetime, str]]) -> str:
    if value is None:
        value = datetime.now(timezone.utc)
    if isinstance(value, str):
        return value
    if value.tzinfo is None:
        value = value.replace(tzinfo=timezone.utc)
    return value.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")


def _json_default(value: Any) -> Any:
    if isinstance(value, datetime):
        return _format_ts(value)
    raise TypeError(f"object of type {type(value).__name__} is not JSON serializable")


def _error_message(status_code: int, body: str) -> str:
    try:
        payload = json.loads(body)
    except json.JSONDecodeError:
        payload = {}
    if isinstance(payload, dict) and payload.get("error"):
        return f"Vigil returned {status_code}: {payload['error']}"
    return f"Vigil returned HTTP {status_code}"


def _required(name: str, value: Optional[str]) -> str:
    cleaned = _clean(value)
    if not cleaned:
        raise VigilConfigError(f"{name} is required")
    return cleaned


def _clean(value: Optional[str]) -> str:
    return str(value).strip() if value is not None else ""


def _set_if_present(target: dict[str, Any], key: str, value: Optional[str]) -> None:
    cleaned = _clean(value)
    if cleaned:
        target[key] = cleaned
