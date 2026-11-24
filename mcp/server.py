"""Production-grade MCP Server for Roaming World.

Covers all 7 Roaming World domains (scene, food, comment, article, audiobook,
plan, ai), all 3 MCP primitives (Tools, Resources, Prompts), protocol version
negotiation (2025-03-26 / 2024-11-05), Streamable HTTP + stdio transport,
httpx connection pooling with retry, Prometheus /metrics, /readyz, and
graceful shutdown.

Optional third-party dependencies (mcp, httpx, uvicorn, prometheus_client) are
guarded so the core MCPHandler, HTTP/stdio JSON-RPC 2.0 server, and unit tests
run cleanly both with full Docker/CI dependencies and offline on Python 3.12 stdlib.
"""
from __future__ import annotations

import argparse
import asyncio
import json
import os
import re
import secrets
import signal
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Callable

try:
    from . import tools as mcp_tools
except ImportError:
    import tools as mcp_tools  # type: ignore[no-redef]

try:
    import httpx  # type: ignore[import-not-found]

    HAS_HTTPX = True
except Exception:
    httpx = None  # type: ignore[assignment]
    HAS_HTTPX = False

try:
    import prometheus_client  # type: ignore[import-not-found]
    from prometheus_client import (  # type: ignore[import-not-found]
        CONTENT_TYPE_LATEST as PROM_CONTENT_TYPE,
        CollectorRegistry,
        Counter,
        Histogram,
        generate_latest,
    )

    HAS_PROMETHEUS = True
except Exception:
    prometheus_client = None  # type: ignore[assignment]
    CollectorRegistry = None  # type: ignore[assignment]
    Counter = None  # type: ignore[assignment]
    Histogram = None  # type: ignore[assignment]
    generate_latest = None  # type: ignore[assignment]
    PROM_CONTENT_TYPE = "text/plain; version=0.0.4; charset=utf-8"
    HAS_PROMETHEUS = False

try:
    from mcp.server import Server as OfficialMCPServer  # type: ignore[import-not-found]
    from mcp import types as mcp_sdk_types  # type: ignore[import-not-found]

    HAS_MCP_SDK = True
except Exception:
    OfficialMCPServer = None  # type: ignore[assignment]
    mcp_sdk_types = None  # type: ignore[assignment]
    HAS_MCP_SDK = False

try:
    import uvicorn  # type: ignore[import-not-found]

    HAS_UVICORN = True
except Exception:
    uvicorn = None  # type: ignore[assignment]
    HAS_UVICORN = False

# Re-export constants/types for backwards compatibility
SUPPORTED_PROTOCOL_VERSIONS = ("2025-03-26", "2024-11-05")
PROTOCOL_VERSION = "2025-03-26"
SERVER_INFO = {"name": "roaming-world-mcp", "version": "1.0.0"}

MAX_BODY_BYTES = 1_048_576  # 1 MiB
MAX_QUERY_LEN = mcp_tools.MAX_QUERY_LEN
EXCERPT_MAX_LEN = mcp_tools.EXCERPT_MAX_LEN
ID_PATTERN = mcp_tools.ID_PATTERN
SESSION_ID_PATTERN = re.compile(r"^[A-Za-z0-9._-]{1,128}\Z")
RETRYABLE_STATUS_CODES = (502, 503, 504)
HISTOGRAM_BUCKETS = (0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0)

InvalidParamsError = mcp_tools.InvalidParamsError
TOOLS = mcp_tools.TOOLS
RESOURCES = mcp_tools.RESOURCES
RESOURCE_TEMPLATES = mcp_tools.RESOURCE_TEMPLATES
PROMPTS = mcp_tools.PROMPTS

_validate_base_url = mcp_tools.validate_base_url
_validate_id = mcp_tools.validate_id
_validate_query = mcp_tools.validate_query
_validate_limit = mcp_tools.validate_limit
_validate_page = mcp_tools.validate_page


def _env(name: str, default: str) -> str:
    return os.getenv(name, default).rstrip("/")


def _escape_prom_label(value: str) -> str:
    return value.replace("\\", "\\\\").replace('"', '\\"').replace("\n", "\\n")


class MCPMetrics:
    """Prometheus metrics recorder with prometheus_client + stdlib fallback."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._calls: dict[tuple[str, str], int] = {}
        self._duration_buckets: dict[str, list[int]] = {}
        self._duration_sum: dict[str, float] = {}
        self._duration_count: dict[str, int] = {}

        self._registry = None
        self._prom_calls = None
        self._prom_duration = None
        if HAS_PROMETHEUS and CollectorRegistry is not None:
            self._registry = CollectorRegistry()
            self._prom_calls = Counter(
                "roaming_mcp_tool_calls_total",
                "Total number of MCP tool calls by tool name and status.",
                ["tool", "status"],
                registry=self._registry,
            )
            self._prom_duration = Histogram(
                "roaming_mcp_tool_duration_seconds",
                "Duration of MCP tool calls in seconds.",
                ["tool"],
                buckets=HISTOGRAM_BUCKETS,
                registry=self._registry,
            )

    def observe_tool_call(self, tool: str, status: str, duration_seconds: float) -> None:
        dur = max(0.0, float(duration_seconds))
        if self._prom_calls is not None and self._prom_duration is not None:
            self._prom_calls.labels(tool=tool, status=status).inc()
            self._prom_duration.labels(tool=tool).observe(dur)
            return
        with self._lock:
            key = (tool, status)
            self._calls[key] = self._calls.get(key, 0) + 1
            buckets = self._duration_buckets.setdefault(
                tool, [0] * (len(HISTOGRAM_BUCKETS) + 1)
            )
            for idx, upper in enumerate(HISTOGRAM_BUCKETS):
                if dur <= upper:
                    buckets[idx] += 1
            buckets[-1] += 1  # +Inf
            self._duration_sum[tool] = self._duration_sum.get(tool, 0.0) + dur
            self._duration_count[tool] = self._duration_count.get(tool, 0) + 1

    def render_prometheus(self) -> tuple[bytes, str]:
        if self._registry is not None and generate_latest is not None:
            return generate_latest(self._registry), PROM_CONTENT_TYPE

        lines: list[str] = [
            "# HELP roaming_mcp_tool_calls_total Total number of MCP tool calls by tool name and status.",
            "# TYPE roaming_mcp_tool_calls_total counter",
        ]
        with self._lock:
            for (tool, status), count in sorted(self._calls.items()):
                t = _escape_prom_label(tool)
                s = _escape_prom_label(status)
                lines.append(
                    f'roaming_mcp_tool_calls_total{{tool="{t}",status="{s}"}} {count}'
                )
            lines.append(
                "# HELP roaming_mcp_tool_duration_seconds Duration of MCP tool calls in seconds."
            )
            lines.append("# TYPE roaming_mcp_tool_duration_seconds histogram")
            for tool in sorted(self._duration_count.keys()):
                t = _escape_prom_label(tool)
                buckets = self._duration_buckets[tool]
                for idx, upper in enumerate(HISTOGRAM_BUCKETS):
                    lines.append(
                        f'roaming_mcp_tool_duration_seconds_bucket{{tool="{t}",le="{upper}"}} {buckets[idx]}'
                    )
                lines.append(
                    f'roaming_mcp_tool_duration_seconds_bucket{{tool="{t}",le="+Inf"}} {buckets[-1]}'
                )
                lines.append(
                    f'roaming_mcp_tool_duration_seconds_sum{{tool="{t}"}} {self._duration_sum[tool]:.6f}'
                )
                lines.append(
                    f'roaming_mcp_tool_duration_seconds_count{{tool="{t}"}} {self._duration_count[tool]}'
                )
        body = ("\n".join(lines) + "\n").encode("utf-8")
        return body, PROM_CONTENT_TYPE


class UpstreamHTTPClient:
    """Pooled HTTP client with retry on 502/503/504 using httpx when available, else urllib."""

    def __init__(self, timeout: float = 10.0, max_retries: int = 2) -> None:
        self.timeout = timeout
        self.max_retries = max_retries
        self._client: Any = None
        self._async_client: Any = None
        self._lock = threading.Lock()

    def _get_sync_client(self) -> Any:
        if not HAS_HTTPX or httpx is None:
            return None
        with self._lock:
            if self._client is None:
                limits = httpx.Limits(
                    max_connections=100,
                    max_keepalive_connections=20,
                )
                self._client = httpx.Client(
                    timeout=httpx.Timeout(self.timeout, connect=3.0),
                    limits=limits,
                )
            return self._client

    def get_async_client(self) -> Any:
        if not HAS_HTTPX or httpx is None:
            return None
        with self._lock:
            if self._async_client is None:
                limits = httpx.Limits(
                    max_connections=100,
                    max_keepalive_connections=20,
                )
                self._async_client = httpx.AsyncClient(
                    timeout=httpx.Timeout(self.timeout, connect=3.0),
                    limits=limits,
                )
            return self._async_client

    def request_json(
        self,
        method: str,
        url: str,
        *,
        headers: dict[str, str] | None = None,
        json_body: dict[str, Any] | None = None,
    ) -> Any:
        req_headers = dict(headers or {})
        can_retry = method.upper() == "GET" or bool(req_headers.get("Idempotency-Key"))
        attempts = (self.max_retries + 1) if can_retry else 1

        client = self._get_sync_client()
        if client is not None:
            last_exc: Exception | None = None
            for attempt in range(attempts):
                try:
                    resp = client.request(
                        method.upper(),
                        url,
                        headers=req_headers,
                        json=json_body,
                    )
                    if resp.status_code in RETRYABLE_STATUS_CODES and attempt + 1 < attempts:
                        time.sleep(0.05 * (attempt + 1))
                        continue
                    resp.raise_for_status()
                    return resp.json()
                except Exception as exc:
                    last_exc = exc
                    if attempt + 1 < attempts and isinstance(exc, httpx.TransportError):
                        time.sleep(0.05 * (attempt + 1))
                        continue
                    raise
            if last_exc is not None:
                raise last_exc

        # Stdlib urllib fallback
        data_bytes: bytes | None = None
        if json_body is not None:
            data_bytes = json.dumps(json_body, ensure_ascii=False).encode("utf-8")
            req_headers.setdefault("Content-Type", "application/json; charset=utf-8")

        for attempt in range(attempts):
            req = urllib.request.Request(
                url,
                data=data_bytes,
                headers=req_headers,
                method=method.upper(),
            )
            try:
                with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                    return json.loads(resp.read().decode("utf-8"))
            except urllib.error.HTTPError as exc:
                if exc.code in RETRYABLE_STATUS_CODES and attempt + 1 < attempts:
                    time.sleep(0.05 * (attempt + 1))
                    continue
                raise
            except urllib.error.URLError:
                if attempt + 1 < attempts:
                    time.sleep(0.05 * (attempt + 1))
                    continue
                raise

        raise RuntimeError("upstream request failed")

    def close(self) -> None:
        with self._lock:
            if self._client is not None:
                try:
                    self._client.close()
                except Exception:
                    pass
                self._client = None


class MCPHandler:
    def __init__(
        self,
        fetch_json: Callable[[str], Any] | None = None,
        request_json: Callable[..., Any] | None = None,
        metrics: MCPMetrics | None = None,
    ) -> None:
        self.scene_api = _validate_base_url(
            "SCENE_API", _env("SCENE_API", "http://127.0.0.1:18001")
        )
        self.food_api = _validate_base_url(
            "FOOD_API", _env("FOOD_API", "http://127.0.0.1:18002")
        )
        self.comment_api = _validate_base_url(
            "COMMENT_API", _env("COMMENT_API", "http://127.0.0.1:18003")
        )
        self.article_api = _validate_base_url(
            "ARTICLE_API", _env("ARTICLE_API", "http://127.0.0.1:18004")
        )
        self.audiobook_api = _validate_base_url(
            "AUDIOBOOK_API", _env("AUDIOBOOK_API", "http://127.0.0.1:18005")
        )
        self.plan_api = _validate_base_url(
            "PLAN_API", _env("PLAN_API", "http://127.0.0.1:18006")
        )
        self.ai_api = _validate_base_url(
            "AI_API", _env("AI_API", "http://127.0.0.1:8000")
        )

        self._custom_fetch_json = fetch_json
        self._custom_request_json = request_json
        self._http_client = UpstreamHTTPClient()
        self.metrics = metrics or MCPMetrics()
        # Keep attribute for backwards compatibility with callers inspecting handler.fetch_json
        self.fetch_json = fetch_json or self._default_fetch_json

    def close(self) -> None:
        self._http_client.close()

    def _default_fetch_json(self, url: str) -> Any:
        return self._http_client.request_json("GET", url)

    @staticmethod
    def _build_forward_headers(
        context: dict[str, Any] | None = None,
        extra: dict[str, str] | None = None,
    ) -> dict[str, str]:
        headers: dict[str, str] = {}
        ctx = context or {}
        if ctx.get("request_id"):
            headers["X-Request-ID"] = str(ctx["request_id"]).strip()
        if ctx.get("traceparent"):
            headers["traceparent"] = str(ctx["traceparent"]).strip()
        if ctx.get("user_id"):
            headers["X-User-ID"] = str(ctx["user_id"]).strip()
        if extra:
            for k, v in extra.items():
                if v:
                    headers[k] = v
        return headers

    def _get(
        self,
        url: str,
        *,
        context: dict[str, Any] | None = None,
        extra_headers: dict[str, str] | None = None,
    ) -> Any:
        headers = self._build_forward_headers(context, extra_headers)
        if self._custom_request_json is not None:
            return self._custom_request_json("GET", url, headers=headers, json_body=None)
        if self._custom_fetch_json is not None:
            return self._custom_fetch_json(url)
        return self._http_client.request_json("GET", url, headers=headers)

    def _post(
        self,
        url: str,
        payload: dict[str, Any],
        *,
        context: dict[str, Any] | None = None,
        extra_headers: dict[str, str] | None = None,
    ) -> Any:
        headers = self._build_forward_headers(context, extra_headers)
        if self._custom_request_json is not None:
            return self._custom_request_json("POST", url, headers=headers, json_body=payload)
        return self._http_client.request_json(
            "POST", url, headers=headers, json_body=payload
        )

    @staticmethod
    def _extract_list(data: Any, *keys: str) -> list[dict[str, Any]]:
        if isinstance(data, list):
            return [item for item in data if isinstance(item, dict)]
        if isinstance(data, dict):
            for key in keys:
                val = data.get(key)
                if isinstance(val, list):
                    return [item for item in val if isinstance(item, dict)]
        return []

    @staticmethod
    def _extract_total(data: Any) -> int | None:
        if isinstance(data, dict):
            for key in ("total", "count", "totalCount"):
                val = data.get(key)
                if isinstance(val, int) and not isinstance(val, bool) and val >= 0:
                    return val
        return None

    # ---- Tool implementations ----

    def _search_scenes(
        self, query: str, page: int, limit: int, context: dict[str, Any] | None
    ) -> dict[str, Any]:
        encoded = urllib.parse.quote(query, safe="")
        data = self._get(
            f"{self.scene_api}/v1/scene/search?keyword={encoded}&page={page}&pagesize={limit}",
            context=context,
        )
        items = self._extract_list(data, "scenes", "items", "data")
        if items:
            projected: list[dict[str, Any]] = []
            any_truncated = False
            for item in items[:limit]:
                proj, trunc = mcp_tools.project_scene(item)
                projected.append(proj)
                any_truncated = any_truncated or trunc
            upstream_total = self._extract_total(data)
            total = (
                upstream_total
                if upstream_total is not None and upstream_total >= len(projected)
                else len(items)
            )
            has_more = ((page - 1) * limit + len(projected)) < total
            return mcp_tools.make_page_envelope(
                projected,
                page=page,
                limit=limit,
                total=total,
                has_more=has_more,
                truncated=any_truncated,
            )

        # Fallback to paginated list + local match when backend search returns no items.
        fallback_data = self._get(
            f"{self.scene_api}/v1/scene?page={page}&pagesize=50", context=context
        )
        fallback_items = self._extract_list(fallback_data, "scenes", "items", "data")
        matched: list[tuple[dict[str, Any], bool]] = []
        for item in fallback_items:
            if mcp_tools.matches_query(
                query,
                item.get("name", ""),
                item.get("describe", ""),
                item.get("content", ""),
                item.get("location", ""),
            ):
                matched.append(mcp_tools.project_scene(item))
        page_slice = matched[:limit]
        projected = [p for p, _ in page_slice]
        any_truncated = any(t for _, t in page_slice)
        return mcp_tools.make_page_envelope(
            projected,
            page=page,
            limit=limit,
            total=len(matched),
            has_more=len(matched) > limit,
            truncated=any_truncated,
        )

    def _search_foods(
        self, query: str, page: int, limit: int, context: dict[str, Any] | None
    ) -> dict[str, Any]:
        data = self._get(
            f"{self.food_api}/v1/food?page={page}&limit=50", context=context
        )
        items = self._extract_list(data, "foods", "items", "data")
        matched: list[tuple[dict[str, Any], bool]] = []
        for item in items:
            if mcp_tools.matches_query(
                query,
                item.get("name", ""),
                item.get("describe", ""),
                item.get("recipe", ""),
                item.get("content", ""),
                item.get("location", ""),
            ):
                matched.append(mcp_tools.project_food(item))
        page_slice = matched[:limit]
        projected = [p for p, _ in page_slice]
        any_truncated = any(t for _, t in page_slice)
        return mcp_tools.make_page_envelope(
            projected,
            page=page,
            limit=limit,
            total=len(matched),
            has_more=len(matched) > limit,
            truncated=any_truncated,
        )

    def _get_random_food(self, context: dict[str, Any] | None) -> Any:
        data = self._get(f"{self.food_api}/v1/food/random", context=context)
        if isinstance(data, dict):
            food_obj = data.get("food") if isinstance(data.get("food"), dict) else data
            proj, was_trunc = mcp_tools.project_food(food_obj)
            if food_obj.get("recipe"):
                recipe, r_trunc = mcp_tools.truncate_text(
                    food_obj.get("recipe"), EXCERPT_MAX_LEN
                )
                proj["recipe"] = recipe
                was_trunc = was_trunc or r_trunc
            proj["truncated"] = was_trunc
            return proj
        return data

    def _search_articles(
        self,
        query: str,
        page: int,
        limit: int,
        sort_by: str,
        context: dict[str, Any] | None,
    ) -> dict[str, Any]:
        data = self._get(
            f"{self.article_api}/v1/article?page={page}&limit=50&way={sort_by}",
            context=context,
        )
        items = self._extract_list(data, "articles", "items", "data")
        matched: list[tuple[dict[str, Any], bool]] = []
        for item in items:
            if mcp_tools.matches_query(
                query,
                item.get("title", ""),
                item.get("name", ""),
                item.get("describe", ""),
                item.get("content", ""),
                item.get("location", ""),
            ):
                matched.append(mcp_tools.project_article(item))
        page_slice = matched[:limit]
        projected = [p for p, _ in page_slice]
        any_truncated = any(t for _, t in page_slice)
        return mcp_tools.make_page_envelope(
            projected,
            page=page,
            limit=limit,
            total=len(matched),
            has_more=len(matched) > limit,
            truncated=any_truncated,
        )

    def _list_article_comments(
        self, aid: str, page: int, limit: int, context: dict[str, Any] | None
    ) -> dict[str, Any]:
        quoted_aid = urllib.parse.quote(aid, safe="")
        data = self._get(
            f"{self.comment_api}/v1/comment?aid={quoted_aid}&page={page}&limit={limit}",
            context=context,
        )
        items = self._extract_list(data, "comments", "items", "data")
        projected: list[dict[str, Any]] = []
        any_truncated = False
        for item in items[:limit]:
            proj, trunc = mcp_tools.project_comment(item)
            projected.append(proj)
            any_truncated = any_truncated or trunc
        upstream_total = self._extract_total(data)
        total = (
            upstream_total
            if upstream_total is not None and upstream_total >= len(projected)
            else len(items)
        )
        has_more = ((page - 1) * limit + len(projected)) < total
        return mcp_tools.make_page_envelope(
            projected,
            page=page,
            limit=limit,
            total=total,
            has_more=has_more,
            truncated=any_truncated,
        )

    def _list_public_plans(
        self, query: str, page: int, limit: int, context: dict[str, Any] | None
    ) -> dict[str, Any]:
        data = self._get(
            f"{self.plan_api}/v1/plan?scope=public&page={page}&limit=50",
            context=context,
        )
        items = self._extract_list(data, "plans", "items", "data")
        matched: list[tuple[dict[str, Any], bool]] = []
        for item in items:
            if not query or mcp_tools.matches_query(
                query, item.get("title", ""), item.get("city", "")
            ):
                matched.append(mcp_tools.project_plan(item))
        page_slice = matched[:limit]
        projected = [p for p, _ in page_slice]
        any_truncated = any(t for _, t in page_slice)
        upstream_total = self._extract_total(data)
        total = (
            upstream_total
            if not query and upstream_total is not None and upstream_total >= len(matched)
            else len(matched)
        )
        has_more = len(matched) > limit or (
            not query and upstream_total is not None and ((page - 1) * 50 + len(projected)) < upstream_total
        )
        return mcp_tools.make_page_envelope(
            projected,
            page=page,
            limit=limit,
            total=total,
            has_more=has_more,
            truncated=any_truncated,
        )

    def _search_audiobooks(
        self, query: str, limit: int, context: dict[str, Any] | None
    ) -> dict[str, Any]:
        data = self._get(f"{self.audiobook_api}/v1/audiobook", context=context)
        items = self._extract_list(data, "audiobooks", "books", "items", "data")
        matched: list[tuple[dict[str, Any], bool]] = []
        for item in items:
            if not query or mcp_tools.matches_query(
                query,
                item.get("name", item.get("title", "")),
                item.get("author", ""),
                item.get("description", item.get("describe", "")),
            ):
                matched.append(mcp_tools.project_audiobook(item))
        page_slice = matched[:limit]
        projected = [p for p, _ in page_slice]
        any_truncated = any(t for _, t in page_slice)
        return mcp_tools.make_page_envelope(
            projected,
            page=1,
            limit=limit,
            total=len(matched),
            has_more=len(matched) > limit,
            truncated=any_truncated,
        )

    def _search_knowledge_base(
        self, query: str, top_k: int, context: dict[str, Any] | None
    ) -> Any:
        extra_headers: dict[str, str] = {}
        ai_token = os.getenv("AI_GATEWAY_TOKEN", "").strip()
        if ai_token:
            extra_headers["X-AI-Gateway-Token"] = ai_token
        payload = {"query": query, "message": query, "top_k": top_k}
        data = self._post(
            f"{self.ai_api}/v1/ai/chat",
            payload,
            context=context,
            extra_headers=extra_headers,
        )
        if not isinstance(data, dict):
            return data
        answer_raw = data.get("answer") or data.get("reply") or data.get("content") or ""
        answer, ans_trunc = mcp_tools.truncate_text(
            answer_raw, mcp_tools.ARTICLE_CONTENT_MAX_LEN
        )
        sources_raw = data.get("sources") or data.get("citations") or data.get("documents") or []
        sources: list[dict[str, Any]] = []
        src_trunc = False
        if isinstance(sources_raw, list):
            for src in sources_raw[:top_k]:
                if isinstance(src, dict):
                    excerpt, was_t = mcp_tools.truncate_text(
                        src.get("excerpt") or src.get("snippet") or src.get("content") or "",
                        EXCERPT_MAX_LEN,
                    )
                    src_trunc = src_trunc or was_t
                    sources.append(
                        {
                            "id": src.get("id", ""),
                            "title": src.get("title", src.get("name", "")),
                            "type": src.get("type", src.get("source", "")),
                            "score": src.get("score"),
                            "excerpt": excerpt,
                        }
                    )
        return {
            "answer": answer,
            "sources": sources,
            "truncated": ans_trunc or src_trunc,
        }

    def _save_travel_plan_draft(
        self, args: dict[str, Any], context: dict[str, Any] | None
    ) -> Any:
        ctx = context or {}
        user_id = str(ctx.get("user_id") or "").strip()
        authorization = str(ctx.get("authorization") or "").strip()
        if not user_id and not authorization:
            raise InvalidParamsError(
                "user identity is required (provide X-User-ID or Authorization header)"
            )

        idempotency_key = mcp_tools.validate_non_empty_str(
            "idempotency_key",
            args.get("idempotency_key"),
            max_len=mcp_tools.MAX_IDEMPOTENCY_KEY_LEN,
        )
        title = mcp_tools.validate_non_empty_str("title", args.get("title"), max_len=200)
        city = mcp_tools.validate_non_empty_str("city", args.get("city"), max_len=100)
        start_str, start_dt = mcp_tools.validate_date("start_date", args.get("start_date"))
        end_str, end_dt = mcp_tools.validate_date("end_date", args.get("end_date"))
        if end_dt < start_dt:
            raise InvalidParamsError("end_date must be on or after start_date")
        budget = mcp_tools.validate_budget(args.get("budget", 0))
        days = args.get("days")
        if days is not None and not isinstance(days, list):
            raise InvalidParamsError("days must be a list when provided")

        body: dict[str, Any] = {
            "title": title,
            "city": city,
            "start_date": start_str,
            "end_date": end_str,
            "budget": budget,
            "days": days if days is not None else [],
            "status": "draft",
        }
        extra_headers: dict[str, str] = {"Idempotency-Key": idempotency_key}
        if authorization:
            extra_headers["Authorization"] = authorization
        return self._post(
            f"{self.plan_api}/v1/plan",
            body,
            context=ctx,
            extra_headers=extra_headers,
        )

    def call_tool(
        self,
        name: str,
        args: dict[str, Any],
        context: dict[str, Any] | None = None,
    ) -> Any:
        if not isinstance(args, dict):
            raise InvalidParamsError("arguments must be an object")

        if name == "search_scenes":
            query = _validate_query(args.get("query"))
            page = _validate_page(args)
            limit = _validate_limit(args, default=5)
            return self._search_scenes(query, page, limit, context)
        if name == "get_scene":
            sid = _validate_id("sid", args.get("sid"))
            return self._get(
                f"{self.scene_api}/v1/scene/{urllib.parse.quote(sid, safe='')}",
                context=context,
            )
        if name == "search_foods":
            query = _validate_query(args.get("query"))
            page = _validate_page(args)
            limit = _validate_limit(args, default=5)
            return self._search_foods(query, page, limit, context)
        if name == "get_random_food":
            return self._get_random_food(context)
        if name == "search_articles":
            query = _validate_query(args.get("query"))
            page = _validate_page(args)
            limit = _validate_limit(args, default=5)
            sort_by = mcp_tools.validate_sort_by(args)
            return self._search_articles(query, page, limit, sort_by, context)
        if name == "get_article":
            aid = _validate_id("aid", args.get("aid"))
            raw = self._get(
                f"{self.article_api}/v1/article/{urllib.parse.quote(aid, safe='')}",
                context=context,
            )
            return mcp_tools.truncate_article_detail(raw)
        if name == "list_article_comments":
            aid = _validate_id("aid", args.get("aid"))
            page = _validate_page(args)
            limit = _validate_limit(args, default=10)
            return self._list_article_comments(aid, page, limit, context)
        if name == "list_public_plans":
            query = _validate_query(args.get("query", ""), required=False)
            page = _validate_page(args)
            limit = _validate_limit(args, default=5)
            return self._list_public_plans(query, page, limit, context)
        if name == "get_plan":
            plan_id = _validate_id("plan_id", args.get("plan_id"))
            return self._get(
                f"{self.plan_api}/v1/plan/{urllib.parse.quote(plan_id, safe='')}",
                context=context,
            )
        if name == "search_audiobooks":
            query = _validate_query(args.get("query", ""), required=False)
            limit = _validate_limit(args, default=5)
            return self._search_audiobooks(query, limit, context)
        if name == "get_audiobook_details":
            bid = _validate_id("bid", args.get("bid"))
            return self._get(
                f"{self.audiobook_api}/v1/audiobook/{urllib.parse.quote(bid, safe='')}",
                context=context,
            )
        if name == "search_knowledge_base":
            query = _validate_query(args.get("query"))
            top_k = mcp_tools.validate_top_k(args, default=5)
            return self._search_knowledge_base(query, top_k, context)
        if name == "save_travel_plan_draft":
            return self._save_travel_plan_draft(args, context)

        raise InvalidParamsError(f"unknown tool: {name}")

    def read_resource(
        self, uri: Any, context: dict[str, Any] | None = None
    ) -> dict[str, Any]:
        kind, ident = mcp_tools.parse_resource_uri(uri)
        quoted = urllib.parse.quote(ident, safe="")
        if kind == "scene":
            data = self._get(f"{self.scene_api}/v1/scene/{quoted}", context=context)
        elif kind == "article":
            raw = self._get(f"{self.article_api}/v1/article/{quoted}", context=context)
            data = mcp_tools.truncate_article_detail(raw)
        elif kind == "plan":
            data = self._get(f"{self.plan_api}/v1/plan/{quoted}", context=context)
        else:
            raise InvalidParamsError(f"unknown resource type: {kind}")
        return {
            "contents": [
                {
                    "uri": f"roaming://{kind}/{ident}",
                    "mimeType": "application/json",
                    "text": json.dumps(data, ensure_ascii=False),
                }
            ]
        }

    @staticmethod
    def _negotiate_protocol_version(params: Any) -> str:
        if isinstance(params, dict):
            requested = params.get("protocolVersion")
            if isinstance(requested, str) and requested in SUPPORTED_PROTOCOL_VERSIONS:
                return requested
        return PROTOCOL_VERSION

    def handle(
        self, req: Any, context: dict[str, Any] | None = None
    ) -> dict[str, Any] | None:
        if not isinstance(req, dict):
            return self._error(None, -32600, "invalid request")
        if req.get("jsonrpc") != "2.0":
            return self._error(req.get("id"), -32600, "invalid jsonrpc version")
        method = req.get("method")
        rid = req.get("id")

        if method == "initialize":
            negotiated = self._negotiate_protocol_version(req.get("params"))
            return self._result(
                rid,
                {
                    "protocolVersion": negotiated,
                    "capabilities": {"tools": {}, "resources": {}, "prompts": {}},
                    "serverInfo": dict(SERVER_INFO),
                },
            )
        if method in ("notifications/initialized", "notifications/cancelled"):
            return None
        if method == "ping":
            return self._result(rid, {})

        # Tools
        if method == "tools/list":
            return self._result(rid, {"tools": TOOLS})
        if method == "tools/call":
            params = req.get("params")
            if not isinstance(params, dict):
                return self._error(rid, -32602, "invalid params")
            name = params.get("name")
            if not isinstance(name, str) or not name:
                return self._error(rid, -32602, "invalid params: tool name is required")
            args = params.get("arguments")
            if args is None:
                args = {}
            if not isinstance(args, dict):
                return self._error(
                    rid, -32602, "invalid params: arguments must be an object"
                )
            start = time.monotonic()
            try:
                value = self.call_tool(name, args, context=context)
            except InvalidParamsError as exc:
                self.metrics.observe_tool_call(
                    name, "invalid_params", time.monotonic() - start
                )
                return self._error(rid, -32602, str(exc))
            except Exception as exc:
                self.metrics.observe_tool_call(
                    name, "error", time.monotonic() - start
                )
                sys.stderr.write(f"mcp: tool {name} failed: {exc}\n")
                return self._result(
                    rid,
                    {
                        "content": [
                            {"type": "text", "text": f"tool execution failed: {name}"}
                        ],
                        "isError": True,
                    },
                )
            self.metrics.observe_tool_call(name, "ok", time.monotonic() - start)
            return self._result(
                rid,
                {
                    "content": [
                        {"type": "text", "text": json.dumps(value, ensure_ascii=False)}
                    ],
                    "isError": False,
                },
            )

        # Resources
        if method == "resources/list":
            return self._result(
                rid,
                {"resources": RESOURCES, "resourceTemplates": RESOURCE_TEMPLATES},
            )
        if method == "resources/templates/list":
            return self._result(rid, {"resourceTemplates": RESOURCE_TEMPLATES})
        if method == "resources/read":
            params = req.get("params")
            if not isinstance(params, dict):
                return self._error(rid, -32602, "invalid params")
            try:
                result = self.read_resource(params.get("uri"), context=context)
            except InvalidParamsError as exc:
                return self._error(rid, -32602, str(exc))
            except Exception as exc:
                sys.stderr.write(f"mcp: resource read failed: {exc}\n")
                return self._error(rid, -32603, "resource read failed")
            return self._result(rid, result)

        # Prompts
        if method == "prompts/list":
            return self._result(rid, {"prompts": PROMPTS})
        if method == "prompts/get":
            params = req.get("params")
            if not isinstance(params, dict):
                return self._error(rid, -32602, "invalid params")
            try:
                result = mcp_tools.render_prompt(
                    params.get("name"), params.get("arguments")
                )
            except InvalidParamsError as exc:
                return self._error(rid, -32602, str(exc))
            return self._result(rid, result)

        return self._error(rid, -32601, f"method not found: {method}")

    @staticmethod
    def _result(rid: Any, result: Any) -> dict[str, Any]:
        return {"jsonrpc": "2.0", "id": rid, "result": result}

    @staticmethod
    def _error(rid: Any, code: int, message: str) -> dict[str, Any]:
        return {"jsonrpc": "2.0", "id": rid, "error": {"code": code, "message": message}}


def build_sdk_server(handler: MCPHandler) -> Any:
    """Build an official mcp.server.Server wired to MCPHandler when PyPI mcp SDK is installed."""
    if not HAS_MCP_SDK or OfficialMCPServer is None or mcp_sdk_types is None:
        return None

    sdk_server = OfficialMCPServer(SERVER_INFO["name"])

    @sdk_server.list_tools()
    async def _list_tools():
        out = []
        for tool in TOOLS:
            ann = tool.get("annotations") or {}
            out.append(
                mcp_sdk_types.Tool(
                    name=tool["name"],
                    description=tool["description"],
                    inputSchema=tool["inputSchema"],
                    annotations=mcp_sdk_types.ToolAnnotations(**ann)
                    if hasattr(mcp_sdk_types, "ToolAnnotations")
                    else None,
                )
            )
        return out

    @sdk_server.call_tool()
    async def _call_tool(name: str, arguments: dict[str, Any] | None):
        start = time.monotonic()
        try:
            value = await asyncio.to_thread(handler.call_tool, name, arguments or {})
        except InvalidParamsError as exc:
            handler.metrics.observe_tool_call(
                name, "invalid_params", time.monotonic() - start
            )
            raise ValueError(str(exc)) from exc
        except Exception as exc:
            handler.metrics.observe_tool_call(name, "error", time.monotonic() - start)
            sys.stderr.write(f"mcp: tool {name} failed: {exc}\n")
            raise RuntimeError(f"tool execution failed: {name}") from exc
        handler.metrics.observe_tool_call(name, "ok", time.monotonic() - start)
        return [
            mcp_sdk_types.TextContent(
                type="text",
                text=json.dumps(value, ensure_ascii=False),
            )
        ]

    @sdk_server.list_resources()
    async def _list_resources():
        return [
            mcp_sdk_types.Resource(
                uri=r["uri"],
                name=r["name"],
                description=r["description"],
                mimeType=r["mimeType"],
            )
            for r in RESOURCES
        ]

    @sdk_server.read_resource()
    async def _read_resource(uri: Any):
        res = await asyncio.to_thread(handler.read_resource, str(uri))
        return res["contents"][0]["text"]

    @sdk_server.list_prompts()
    async def _list_prompts():
        return [
            mcp_sdk_types.Prompt(
                name=p["name"],
                description=p["description"],
                arguments=[
                    mcp_sdk_types.PromptArgument(
                        name=a["name"],
                        description=a["description"],
                        required=a.get("required", False),
                    )
                    for a in p.get("arguments", [])
                ],
            )
            for p in PROMPTS
        ]

    @sdk_server.get_prompt()
    async def _get_prompt(name: str, arguments: dict[str, str] | None):
        rendered = mcp_tools.render_prompt(name, arguments or {})
        return mcp_sdk_types.GetPromptResult(
            description=rendered["description"],
            messages=[
                mcp_sdk_types.PromptMessage(
                    role=m["role"],
                    content=mcp_sdk_types.TextContent(
                        type="text", text=m["content"]["text"]
                    ),
                )
                for m in rendered["messages"]
            ],
        )

    return sdk_server


def _is_origin_allowed(origin: str | None) -> bool:
    if not origin:
        return True
    cleaned = origin.strip().rstrip("/")
    configured = os.getenv("MCP_ALLOWED_ORIGINS", "").strip()
    if configured:
        allowed = {o.strip().rstrip("/") for o in configured.split(",") if o.strip()}
        return cleaned in allowed
    parsed = urllib.parse.urlparse(cleaned)
    if parsed.scheme not in ("http", "https"):
        return False
    if parsed.hostname not in ("localhost", "127.0.0.1", "::1"):
        return False
    if (
        parsed.path not in ("", "/")
        or parsed.query
        or parsed.fragment
        or parsed.username
        or parsed.password
    ):
        return False
    return True


def _check_token(headers: Any) -> bool:
    expected = os.getenv("MCP_TOKEN", "").strip()
    if not expected:
        return False
    provided = (headers.get("X-MCP-Token") or "").strip()
    if not provided:
        auth = (headers.get("Authorization") or "").strip()
        if auth.lower().startswith("bearer "):
            provided = auth[7:].strip()
    return bool(
        provided
        and secrets.compare_digest(provided.encode("utf-8"), expected.encode("utf-8"))
    )


def _extract_request_context(headers: Any) -> dict[str, Any]:
    ctx: dict[str, Any] = {}
    request_id = (headers.get("X-Request-ID") or "").strip()
    if request_id:
        ctx["request_id"] = request_id
    traceparent = (headers.get("traceparent") or "").strip()
    if traceparent:
        ctx["traceparent"] = traceparent
    user_id = (headers.get("X-User-ID") or "").strip()
    if user_id:
        ctx["user_id"] = user_id

    # Extract user Authorization header without confusing it with MCP_TOKEN auth
    user_auth = (headers.get("X-User-Authorization") or "").strip()
    if not user_auth:
        auth = (headers.get("Authorization") or "").strip()
        expected_mcp = os.getenv("MCP_TOKEN", "").strip()
        if auth:
            is_mcp_bearer = False
            if auth.lower().startswith("bearer ") and expected_mcp:
                token_part = auth[7:].strip()
                is_mcp_bearer = secrets.compare_digest(
                    token_part.encode("utf-8"), expected_mcp.encode("utf-8")
                )
            if not is_mcp_bearer:
                user_auth = auth
    if user_auth:
        ctx["authorization"] = user_auth
    return ctx


def make_http_server(host: str, port: int, handler: MCPHandler | None = None):
    mcp_handler = handler or MCPHandler()
    sessions: set[str] = set()
    sessions_lock = threading.Lock()

    class HTTPHandler(BaseHTTPRequestHandler):
        def _json(
            self,
            code: int,
            body: dict[str, Any],
            extra_headers: dict[str, str] | None = None,
        ) -> None:
            raw = json.dumps(body, ensure_ascii=False).encode("utf-8")
            self.send_response(code)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(raw)))
            for k, v in (extra_headers or {}).items():
                self.send_header(k, v)
            self.end_headers()
            self.wfile.write(raw)

        def _resolve_session_id(self) -> str:
            incoming = (self.headers.get("Mcp-Session-Id") or "").strip()
            if incoming and SESSION_ID_PATTERN.fullmatch(incoming):
                with sessions_lock:
                    sessions.add(incoming)
                return incoming
            new_sid = secrets.token_hex(16)
            with sessions_lock:
                sessions.add(new_sid)
            return new_sid

        def do_GET(self) -> None:
            path = urllib.parse.urlsplit(self.path).path
            if path == "/healthz":
                self._json(200, {"status": "ok"})
                return
            if path == "/readyz":
                if os.getenv("MCP_TOKEN", "").strip():
                    self._json(200, {"status": "ready"})
                else:
                    self._json(
                        503,
                        {"status": "not ready", "reason": "MCP_TOKEN not configured"},
                    )
                return
            if path == "/metrics":
                payload, content_type = mcp_handler.metrics.render_prometheus()
                self.send_response(200)
                self.send_header("Content-Type", content_type)
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)
                return
            self._json(404, {"error": "not found"})

        def do_DELETE(self) -> None:
            path = urllib.parse.urlsplit(self.path).path
            if path != "/mcp":
                self._json(404, {"error": "not found"})
                return
            if not _is_origin_allowed(self.headers.get("Origin")):
                self._json(403, {"error": "forbidden origin"})
                return
            if not _check_token(self.headers):
                self._json(401, {"error": "unauthorized"})
                return
            sid = (self.headers.get("Mcp-Session-Id") or "").strip()
            if sid:
                with sessions_lock:
                    sessions.discard(sid)
            self.send_response(204)
            if sid and SESSION_ID_PATTERN.fullmatch(sid):
                self.send_header("Mcp-Session-Id", sid)
            self.end_headers()

        def _drain_small_body(self) -> None:
            raw_length = self.headers.get("Content-Length")
            if raw_length is None:
                return
            try:
                length = int(raw_length)
            except ValueError:
                return
            if 0 < length <= 65536:
                try:
                    self.rfile.read(length)
                except Exception:
                    pass

        def do_POST(self) -> None:
            path = urllib.parse.urlsplit(self.path).path
            if path != "/mcp":
                self._drain_small_body()
                self._json(404, {"error": "not found"})
                return
            if not _is_origin_allowed(self.headers.get("Origin")):
                self._drain_small_body()
                self._json(403, {"error": "forbidden origin"})
                return
            if not _check_token(self.headers):
                self._drain_small_body()
                self._json(401, {"error": "unauthorized"})
                return
            raw_length = self.headers.get("Content-Length")
            if raw_length is None:
                self._json(400, {"error": "missing content-length"})
                return
            try:
                length = int(raw_length)
            except ValueError:
                self._json(400, {"error": "invalid content-length"})
                return
            if length <= 0:
                self._json(400, {"error": "invalid content-length"})
                return
            if length > MAX_BODY_BYTES:
                self._json(413, {"error": "payload too large"})
                return
            try:
                raw_body = self.rfile.read(length)
                req = json.loads(raw_body.decode("utf-8"))
            except Exception:
                self._json(
                    400,
                    {
                        "jsonrpc": "2.0",
                        "id": None,
                        "error": {"code": -32700, "message": "parse error"},
                    },
                )
                return

            session_id = self._resolve_session_id()
            context = _extract_request_context(self.headers)
            res = mcp_handler.handle(req, context=context)
            if res is None:
                self.send_response(204)
                self.send_header("Mcp-Session-Id", session_id)
                self.end_headers()
                return
            self._json(200, res, extra_headers={"Mcp-Session-Id": session_id})

        def log_message(self, fmt: str, *args: Any) -> None:
            sys.stderr.write("mcp: " + (fmt % args) + "\n")

    server = ThreadingHTTPServer((host, port), HTTPHandler)
    server.mcp_handler = mcp_handler  # type: ignore[attr-defined]
    server.mcp_sessions = sessions  # type: ignore[attr-defined]
    return server


def _run_stdio(handler: MCPHandler) -> None:
    if HAS_MCP_SDK:
        try:
            from mcp.server.stdio import stdio_server  # type: ignore[import-not-found]

            sdk_server = build_sdk_server(handler)
            if sdk_server is not None:
                async def _serve_sdk_stdio() -> None:
                    async with stdio_server() as (read_stream, write_stream):
                        await sdk_server.run(
                            read_stream,
                            write_stream,
                            sdk_server.create_initialization_options(),
                        )

                asyncio.run(_serve_sdk_stdio())
                return
        except Exception as exc:
            sys.stderr.write(f"mcp: sdk stdio unavailable, falling back to json-rpc: {exc}\n")

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except Exception:
            res: dict[str, Any] | None = {
                "jsonrpc": "2.0",
                "id": None,
                "error": {"code": -32700, "message": "parse error"},
            }
        else:
            res = handler.handle(req)
        if res is not None:
            sys.stdout.write(json.dumps(res, ensure_ascii=False) + "\n")
            sys.stdout.flush()


def main() -> None:
    parser = argparse.ArgumentParser(description="Roaming World MCP Server")
    parser.add_argument("--stdio", action="store_true", help="Run in stdio JSON-RPC / MCP SDK mode")
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=8000)
    args = parser.parse_args()

    handler = MCPHandler()

    if args.stdio:
        try:
            _run_stdio(handler)
        finally:
            handler.close()
        return

    if not os.getenv("MCP_TOKEN", "").strip():
        handler.close()
        sys.stderr.write("mcp: MCP_TOKEN must be set when running in HTTP mode\n")
        sys.exit(1)

    server = make_http_server(args.host, args.port, handler=handler)

    def _graceful_shutdown(signum: int, _frame: Any) -> None:
        sys.stderr.write(f"mcp: received signal {signum}, shutting down gracefully\n")
        threading.Thread(target=server.shutdown, daemon=True).start()

    for sig_name in ("SIGTERM", "SIGINT"):
        sig = getattr(signal, sig_name, None)
        if sig is not None:
            try:
                signal.signal(sig, _graceful_shutdown)
            except (ValueError, OSError):
                pass

    try:
        server.serve_forever()
    finally:
        server.server_close()
        handler.close()


if __name__ == "__main__":
    main()
