"""Prometheus metrics wrapper with clean stdlib in-memory fallback.

Supports both ``prometheus_client`` (Docker/CI) and pure Python 3.12 stdlib
rendering valid Prometheus text exposition format 0.0.4.
"""
from __future__ import annotations

import threading
from collections import defaultdict

try:
    from prometheus_client import (
        CONTENT_TYPE_LATEST,
        CollectorRegistry,
        Counter,
        Gauge,
        Histogram,
        generate_latest,
    )
    HAS_PROMETHEUS = True
except ImportError:
    HAS_PROMETHEUS = False
    CONTENT_TYPE_LATEST = "text/plain; version=0.0.4; charset=utf-8"


_HISTOGRAM_BUCKETS = (0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0)


def _escape_label(val: str) -> str:
    return str(val).replace("\\", "\\\\").replace('"', '\\"').replace("\n", "\\n")


def _format_labels(labels: dict[str, str]) -> str:
    if not labels:
        return ""
    parts = [f'{k}="{_escape_label(v)}"' for k, v in labels.items()]
    return "{" + ",".join(parts) + "}"


class MetricsRegistry:
    """Unified Prometheus metrics facade for the AI engine."""

    def __init__(self):
        self._lock = threading.Lock()
        if HAS_PROMETHEUS:
            self._registry = CollectorRegistry()
            self.http_requests = Counter(
                "ai_http_requests_total",
                "Total AI HTTP requests by route, method, and status.",
                ["route", "method", "status"],
                registry=self._registry,
            )
            self.http_duration = Histogram(
                "ai_http_request_duration_seconds",
                "AI HTTP request latency in seconds.",
                ["route", "method"],
                buckets=_HISTOGRAM_BUCKETS,
                registry=self._registry,
            )
            self.llm_requests = Counter(
                "ai_llm_requests_total",
                "Total LLM requests by provider, role, and status.",
                ["provider", "role", "status"],
                registry=self._registry,
            )
            self.llm_tokens = Counter(
                "ai_llm_tokens_total",
                "Total LLM tokens consumed by provider and type.",
                ["provider", "type"],
                registry=self._registry,
            )
            self.rag_top1_score = Gauge(
                "ai_rag_top1_score",
                "Latest RAG top-1 retrieval relevance score.",
                registry=self._registry,
            )
            self.agent_revisions = Counter(
                "ai_agent_revisions_total",
                "Total bounded self-correction revisions executed by the multi-agent orchestrator.",
                registry=self._registry,
            )
            # Legacy compatibility counters
            self.legacy_requests = Counter(
                "ai_requests_total",
                "Total AI HTTP requests.",
                registry=self._registry,
            )
            self.legacy_errors = Counter(
                "ai_errors_total",
                "Total AI 5xx responses.",
                registry=self._registry,
            )
            self.legacy_duration_sum = Counter(
                "ai_request_duration_seconds_total",
                "Cumulative request duration.",
                registry=self._registry,
            )
        else:
            self._registry = None
            self._http_requests: dict[tuple[str, str, str], int] = defaultdict(int)
            self._http_hist_buckets: dict[tuple[str, str], list[int]] = {}
            self._http_hist_sum: dict[tuple[str, str], float] = defaultdict(float)
            self._http_hist_count: dict[tuple[str, str], int] = defaultdict(int)
            self._llm_requests: dict[tuple[str, str, str], int] = defaultdict(int)
            self._llm_tokens: dict[tuple[str, str], int] = defaultdict(int)
            self._rag_top1_score: float = 0.0
            self._agent_revisions: int = 0
            self._legacy_requests: int = 0
            self._legacy_errors: int = 0
            self._legacy_duration_sum: float = 0.0

    def record_http_request(self, route: str, method: str, status: int, duration_seconds: float) -> None:
        status_str = str(status)
        dur = max(0.0, float(duration_seconds))
        if HAS_PROMETHEUS:
            self.http_requests.labels(route=route, method=method, status=status_str).inc()
            self.http_duration.labels(route=route, method=method).observe(dur)
            self.legacy_requests.inc()
            if status >= 500:
                self.legacy_errors.inc()
            self.legacy_duration_sum.inc(dur)
            return

        with self._lock:
            self._http_requests[(route, method, status_str)] += 1
            hkey = (route, method)
            buckets = self._http_hist_buckets.get(hkey)
            if buckets is None:
                buckets = [0] * (len(_HISTOGRAM_BUCKETS) + 1)
                self._http_hist_buckets[hkey] = buckets
            for idx, bound in enumerate(_HISTOGRAM_BUCKETS):
                if dur <= bound:
                    buckets[idx] += 1
            buckets[-1] += 1  # +Inf
            self._http_hist_sum[hkey] += dur
            self._http_hist_count[hkey] += 1
            self._legacy_requests += 1
            if status >= 500:
                self._legacy_errors += 1
            self._legacy_duration_sum += dur

    def record_llm_request(
        self,
        provider: str,
        role: str,
        status: str,
        prompt_tokens: int = 0,
        completion_tokens: int = 0,
    ) -> None:
        p_tok = max(0, int(prompt_tokens or 0))
        c_tok = max(0, int(completion_tokens or 0))
        if HAS_PROMETHEUS:
            self.llm_requests.labels(provider=provider, role=role, status=status).inc()
            if p_tok:
                self.llm_tokens.labels(provider=provider, type="prompt").inc(p_tok)
            if c_tok:
                self.llm_tokens.labels(provider=provider, type="completion").inc(c_tok)
            return

        with self._lock:
            self._llm_requests[(provider, role, status)] += 1
            if p_tok:
                self._llm_tokens[(provider, "prompt")] += p_tok
            if c_tok:
                self._llm_tokens[(provider, "completion")] += c_tok

    def record_rag_top1_score(self, score: float) -> None:
        val = float(score or 0.0)
        if HAS_PROMETHEUS:
            self.rag_top1_score.set(val)
            return
        with self._lock:
            self._rag_top1_score = val

    def record_agent_revision(self, count: int = 1) -> None:
        n = max(0, int(count))
        if not n:
            return
        if HAS_PROMETHEUS:
            self.agent_revisions.inc(n)
            return
        with self._lock:
            self._agent_revisions += n

    def render(self) -> tuple[str, str]:
        if HAS_PROMETHEUS:
            return generate_latest(self._registry).decode("utf-8"), CONTENT_TYPE_LATEST

        lines: list[str] = []
        with self._lock:
            lines.append("# HELP ai_http_requests_total Total AI HTTP requests by route, method, and status.")
            lines.append("# TYPE ai_http_requests_total counter")
            for (route, method, status), count in sorted(self._http_requests.items()):
                lbl = _format_labels({"route": route, "method": method, "status": status})
                lines.append(f"ai_http_requests_total{lbl} {count}")

            lines.append("# HELP ai_http_request_duration_seconds AI HTTP request latency in seconds.")
            lines.append("# TYPE ai_http_request_duration_seconds histogram")
            for (route, method), buckets in sorted(self._http_hist_buckets.items()):
                for idx, bound in enumerate(_HISTOGRAM_BUCKETS):
                    lbl = _format_labels({"route": route, "method": method, "le": str(bound)})
                    lines.append(f"ai_http_request_duration_seconds_bucket{lbl} {buckets[idx]}")
                inf_lbl = _format_labels({"route": route, "method": method, "le": "+Inf"})
                lines.append(f"ai_http_request_duration_seconds_bucket{inf_lbl} {buckets[-1]}")
                base_lbl = _format_labels({"route": route, "method": method})
                lines.append(
                    f"ai_http_request_duration_seconds_sum{base_lbl} {self._http_hist_sum[(route, method)]:.6f}"
                )
                lines.append(
                    f"ai_http_request_duration_seconds_count{base_lbl} {self._http_hist_count[(route, method)]}"
                )

            lines.append("# HELP ai_llm_requests_total Total LLM requests by provider, role, and status.")
            lines.append("# TYPE ai_llm_requests_total counter")
            for (provider, role, status), count in sorted(self._llm_requests.items()):
                lbl = _format_labels({"provider": provider, "role": role, "status": status})
                lines.append(f"ai_llm_requests_total{lbl} {count}")

            lines.append("# HELP ai_llm_tokens_total Total LLM tokens consumed by provider and type.")
            lines.append("# TYPE ai_llm_tokens_total counter")
            for (provider, tok_type), count in sorted(self._llm_tokens.items()):
                lbl = _format_labels({"provider": provider, "type": tok_type})
                lines.append(f"ai_llm_tokens_total{lbl} {count}")

            lines.append("# HELP ai_rag_top1_score Latest RAG top-1 retrieval relevance score.")
            lines.append("# TYPE ai_rag_top1_score gauge")
            lines.append(f"ai_rag_top1_score {self._rag_top1_score:.6f}")

            lines.append("# HELP ai_agent_revisions_total Total bounded self-correction revisions.")
            lines.append("# TYPE ai_agent_revisions_total counter")
            lines.append(f"ai_agent_revisions_total {self._agent_revisions}")

            lines.append("# HELP ai_requests_total Total AI HTTP requests.")
            lines.append("# TYPE ai_requests_total counter")
            lines.append(f"ai_requests_total {self._legacy_requests}")

            lines.append("# HELP ai_errors_total Total AI 5xx responses.")
            lines.append("# TYPE ai_errors_total counter")
            lines.append(f"ai_errors_total {self._legacy_errors}")

            lines.append("# HELP ai_request_duration_seconds_total Cumulative request duration.")
            lines.append("# TYPE ai_request_duration_seconds_total counter")
            lines.append(f"ai_request_duration_seconds_total {self._legacy_duration_sum:.6f}")

        return "\n".join(lines) + "\n", CONTENT_TYPE_LATEST


METRICS = MetricsRegistry()


def record_http_request(route: str, method: str, status: int, duration_seconds: float) -> None:
    METRICS.record_http_request(route, method, status, duration_seconds)


def record_llm_request(
    provider: str,
    role: str,
    status: str,
    prompt_tokens: int = 0,
    completion_tokens: int = 0,
) -> None:
    METRICS.record_llm_request(provider, role, status, prompt_tokens, completion_tokens)


def record_rag_top1_score(score: float) -> None:
    METRICS.record_rag_top1_score(score)


def record_agent_revision(count: int = 1) -> None:
    METRICS.record_agent_revision(count)


def render_metrics() -> tuple[str, str]:
    return METRICS.render()
