"""OpenAI-compatible provider clients with async tiered routing and circuit breaker.

Keys are read from environment variables only. Never put them in source,
tests, Dockerfiles, manifests, or logs.
"""
from __future__ import annotations

import asyncio
import json
import os
import random
import threading
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any, Callable

try:
    import httpx
except ImportError:  # stdlib-only local test environments
    httpx = None  # type: ignore[assignment]


RETRYABLE_STATUS_CODES = frozenset({408, 429, 500, 502, 503, 504})

ROLE_PROVIDER_PREFERENCES: dict[str, tuple[str, ...]] = {
    "router": ("deepseek", "qwen", "xiaomi"),
    "planner": ("qwen", "deepseek", "xiaomi"),
    "reviewer": ("deepseek", "qwen", "xiaomi"),
    "verifier": ("deepseek", "qwen", "xiaomi"),
    "writer": ("xiaomi", "qwen", "deepseek"),
}


class ProviderError(RuntimeError):
    pass


@dataclass(frozen=True)
class Provider:
    name: str
    key_env: str
    base_env: str
    model_env: str
    default_base: str
    default_model: str
    max_tokens: int = 900

    @property
    def api_key(self) -> str:
        return os.getenv(self.key_env, "")

    @property
    def base_url(self) -> str:
        return os.getenv(self.base_env, self.default_base).rstrip("/")

    @property
    def model(self) -> str:
        return os.getenv(self.model_env, self.default_model)

    @property
    def configured(self) -> bool:
        return bool(self.api_key)


@dataclass
class LLMResult:
    text: str
    prompt_tokens: int = 0
    completion_tokens: int = 0
    total_tokens: int = 0
    provider: str = ""
    model: str = ""
    latency_ms: float = 0.0


class ProviderCircuitBreaker:
    """Thread-safe Closed -> Open -> HalfOpen circuit breaker per provider."""

    CLOSED = "closed"
    OPEN = "open"
    HALF_OPEN = "half_open"

    def __init__(
        self,
        failure_threshold: int = 3,
        cooldown_seconds: float = 30.0,
        *,
        clock: Callable[[], float] = time.monotonic,
    ):
        self.failure_threshold = max(1, int(failure_threshold))
        self.cooldown_seconds = max(0.0, float(cooldown_seconds))
        self._clock = clock
        self._lock = threading.Lock()
        self._state = self.CLOSED
        self._failure_count = 0
        self._opened_at = 0.0

    @property
    def state(self) -> str:
        with self._lock:
            if self._state == self.OPEN and (self._clock() - self._opened_at) >= self.cooldown_seconds:
                self._state = self.HALF_OPEN
            return self._state

    @property
    def failure_count(self) -> int:
        with self._lock:
            return self._failure_count

    def is_available(self) -> bool:
        with self._lock:
            if self._state != self.OPEN:
                return True
            return (self._clock() - self._opened_at) >= self.cooldown_seconds

    def allow_request(self) -> bool:
        with self._lock:
            if self._state == self.CLOSED:
                return True
            if self._state == self.OPEN:
                if (self._clock() - self._opened_at) >= self.cooldown_seconds:
                    self._state = self.HALF_OPEN
                    return True
                return False
            # HALF_OPEN: allow probe request
            return True

    def record_success(self) -> None:
        with self._lock:
            self._failure_count = 0
            self._opened_at = 0.0
            self._state = self.CLOSED

    def record_failure(self) -> None:
        with self._lock:
            self._failure_count += 1
            if self._state == self.HALF_OPEN or self._failure_count >= self.failure_threshold:
                self._state = self.OPEN
                self._opened_at = self._clock()

    def reset(self) -> None:
        with self._lock:
            self._failure_count = 0
            self._opened_at = 0.0
            self._state = self.CLOSED


PROVIDERS = {
    "qwen": Provider(
        "qwen", "QWEN_API_KEY", "QWEN_BASE_URL", "QWEN_MODEL",
        "https://maas.qianwenaiapi.com/compatible-mode/v1",
        "qwen3.8-2.4t-a95b",
        500,
    ),
    "deepseek": Provider(
        "deepseek", "DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL", "DEEPSEEK_MODEL",
        "https://api.deepseek.com", "deepseek-flash", 900,
    ),
    "xiaomi": Provider(
        "xiaomi", "XIAOMI_API_KEY", "XIAOMI_BASE_URL", "XIAOMI_MODEL",
        "https://api.xiaomimimo.com/v1", "mimo-v2.6-pro", 900,
    ),
}

CIRCUIT_BREAKERS: dict[str, ProviderCircuitBreaker] = {
    name: ProviderCircuitBreaker() for name in PROVIDERS
}
_CB_LOCK = threading.Lock()


def get_circuit_breaker(name: str) -> ProviderCircuitBreaker:
    with _CB_LOCK:
        cb = CIRCUIT_BREAKERS.get(name)
        if cb is None:
            cb = ProviderCircuitBreaker()
            CIRCUIT_BREAKERS[name] = cb
        return cb


def _parse_retry_after(header_value: str | None) -> float | None:
    if not header_value:
        return None
    try:
        val = float(header_value.strip())
        if val >= 0:
            return min(val, 30.0)
    except ValueError:
        pass
    return None


def _compute_backoff(attempt: int, retry_after: str | None = None, base_delay: float = 0.5) -> float:
    parsed = _parse_retry_after(retry_after)
    if parsed is not None:
        return parsed
    jitter = random.uniform(0.0, 0.25)
    return min(base_delay * (2 ** attempt) + jitter, 15.0)


def _extract_llm_content(data: dict[str, Any]) -> tuple[str, int, int, int]:
    """Safely parse chat completion JSON including thinking models and token usage."""
    choices = data.get("choices") or []
    text = ""
    if choices and isinstance(choices[0], dict):
        msg = choices[0].get("message") or {}
        if isinstance(msg, dict):
            content = msg.get("content")
            if isinstance(content, list):
                parts = []
                for part in content:
                    if isinstance(part, dict):
                        parts.append(str(part.get("text") or ""))
                    elif isinstance(part, str):
                        parts.append(part)
                text = "".join(parts)
            elif isinstance(content, str):
                text = content
            # Thinking models may return content: null and populate reasoning_content
            if not text.strip():
                reasoning = msg.get("reasoning_content")
                if isinstance(reasoning, str) and reasoning.strip():
                    text = reasoning

    usage = data.get("usage") or {}
    prompt_tokens = int(usage.get("prompt_tokens") or 0) if isinstance(usage, dict) else 0
    completion_tokens = int(usage.get("completion_tokens") or 0) if isinstance(usage, dict) else 0
    total_tokens = (
        int(usage.get("total_tokens") or (prompt_tokens + completion_tokens))
        if isinstance(usage, dict)
        else (prompt_tokens + completion_tokens)
    )
    return text, prompt_tokens, completion_tokens, total_tokens


def select_provider(role: str) -> Provider:
    """Select the preferred configured provider for a role, falling back gracefully."""
    prefs = ROLE_PROVIDER_PREFERENCES.get((role or "").lower(), ())
    ordered_names: list[str] = []
    for name in prefs:
        if name in PROVIDERS and name not in ordered_names:
            ordered_names.append(name)
    for name in PROVIDERS:
        if name not in ordered_names:
            ordered_names.append(name)

    configured = [PROVIDERS[n] for n in ordered_names if PROVIDERS[n].configured]
    if not configured:
        raise ProviderError("no LLM provider is configured")

    for provider in configured:
        if get_circuit_breaker(provider.name).is_available():
            return provider
    return configured[0]


def chat(provider: Provider, messages: list[dict], max_tokens: int | None = None, timeout: int = 120) -> str:
    if not provider.configured:
        raise ProviderError(f"{provider.name} key is not configured")
    body = {
        "model": provider.model,
        "messages": messages,
        "temperature": 0.2,
        "max_tokens": max_tokens or provider.max_tokens,
        "stream": False,
    }
    req = urllib.request.Request(
        provider.base_url + "/chat/completions",
        data=json.dumps(body).encode("utf-8"),
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {provider.api_key}",
        },
        method="POST",
    )
    last_exc = None
    for attempt in range(2):
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                data = json.loads(resp.read().decode("utf-8"))
            text, _, _, _ = _extract_llm_content(data)
            return text
        except urllib.error.HTTPError as exc:
            # Do not retry auth/quota/model errors.
            if 400 <= exc.code < 500:
                raise ProviderError(f"{provider.name} HTTP {exc.code}") from exc
            last_exc = exc
        except Exception as exc:
            last_exc = exc
        if attempt == 0:
            time.sleep(2)
    raise ProviderError(f"{provider.name} request failed: {last_exc}")


def _sync_chat_result(
    provider: Provider,
    messages: list[dict],
    *,
    max_tokens: int | None = None,
    timeout: float = 45.0,
    max_retries: int = 2,
    sleep_fn: Callable[[float], None] = time.sleep,
) -> LLMResult:
    if not provider.configured:
        raise ProviderError(f"{provider.name} key is not configured")
    cb = get_circuit_breaker(provider.name)
    if not cb.allow_request():
        raise ProviderError(f"{provider.name} circuit breaker is open")

    started = time.perf_counter()
    body = {
        "model": provider.model,
        "messages": messages,
        "temperature": 0.2,
        "max_tokens": max_tokens or provider.max_tokens,
        "stream": False,
    }
    req = urllib.request.Request(
        provider.base_url + "/chat/completions",
        data=json.dumps(body).encode("utf-8"),
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {provider.api_key}",
        },
        method="POST",
    )
    last_exc: Exception | None = None
    for attempt in range(max_retries + 1):
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                data = json.loads(resp.read().decode("utf-8"))
            text, p_tok, c_tok, t_tok = _extract_llm_content(data)
            cb.record_success()
            return LLMResult(
                text=text,
                prompt_tokens=p_tok,
                completion_tokens=c_tok,
                total_tokens=t_tok,
                provider=provider.name,
                model=provider.model,
                latency_ms=round((time.perf_counter() - started) * 1000.0, 2),
            )
        except urllib.error.HTTPError as exc:
            if exc.code in RETRYABLE_STATUS_CODES and attempt < max_retries:
                retry_after = exc.headers.get("Retry-After") if exc.headers else None
                sleep_fn(_compute_backoff(attempt, retry_after))
                last_exc = exc
                continue
            cb.record_failure()
            raise ProviderError(f"{provider.name} HTTP {exc.code}") from exc
        except Exception as exc:
            last_exc = exc
            if attempt < max_retries:
                sleep_fn(_compute_backoff(attempt))
                continue
            cb.record_failure()
            raise ProviderError(f"{provider.name} request failed: {exc}") from exc

    cb.record_failure()
    raise ProviderError(f"{provider.name} request failed: {last_exc}")


async def achat(
    provider: Provider,
    messages: list[dict],
    *,
    max_tokens: int | None = None,
    timeout: float = 45.0,
    max_retries: int = 2,
    client: Any = None,
    sleep_fn: Callable[[float], Any] = asyncio.sleep,
) -> LLMResult:
    """Async chat completion with circuit breaker, exponential backoff + jitter, and Retry-After."""
    if not provider.configured:
        raise ProviderError(f"{provider.name} key is not configured")

    cb = get_circuit_breaker(provider.name)
    if not cb.allow_request():
        raise ProviderError(f"{provider.name} circuit breaker is open")

    if client is None and httpx is None:
        return await asyncio.to_thread(
            _sync_chat_result,
            provider,
            messages,
            max_tokens=max_tokens,
            timeout=timeout,
            max_retries=max_retries,
        )

    started = time.perf_counter()
    body = {
        "model": provider.model,
        "messages": messages,
        "temperature": 0.2,
        "max_tokens": max_tokens or provider.max_tokens,
        "stream": False,
    }
    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {provider.api_key}",
    }
    url = provider.base_url + "/chat/completions"

    async def _execute(http_client: Any) -> LLMResult:
        last_exc: Exception | None = None
        for attempt in range(max_retries + 1):
            try:
                resp = await http_client.post(url, json=body, headers=headers, timeout=timeout)
                status = int(resp.status_code)
                if 200 <= status < 300:
                    data = resp.json()
                    text, p_tok, c_tok, t_tok = _extract_llm_content(data)
                    cb.record_success()
                    return LLMResult(
                        text=text,
                        prompt_tokens=p_tok,
                        completion_tokens=c_tok,
                        total_tokens=t_tok,
                        provider=provider.name,
                        model=provider.model,
                        latency_ms=round((time.perf_counter() - started) * 1000.0, 2),
                    )
                if status in RETRYABLE_STATUS_CODES and attempt < max_retries:
                    retry_after = resp.headers.get("Retry-After") if getattr(resp, "headers", None) else None
                    await sleep_fn(_compute_backoff(attempt, retry_after))
                    last_exc = ProviderError(f"{provider.name} HTTP {status}")
                    continue
                cb.record_failure()
                raise ProviderError(f"{provider.name} HTTP {status}")
            except ProviderError:
                raise
            except Exception as exc:
                last_exc = exc
                if attempt < max_retries:
                    await sleep_fn(_compute_backoff(attempt))
                    continue
                cb.record_failure()
                raise ProviderError(f"{provider.name} request failed: {exc}") from exc
        cb.record_failure()
        raise ProviderError(f"{provider.name} request failed: {last_exc}")

    if client is not None:
        return await _execute(client)

    async with httpx.AsyncClient(timeout=timeout) as ac:
        return await _execute(ac)


def provider_status() -> dict:
    return {
        name: {
            "configured": p.configured,
            "model": p.model,
            "base_url": p.base_url,
            "circuit_state": get_circuit_breaker(name).state,
        }
        for name, p in PROVIDERS.items()
    }
