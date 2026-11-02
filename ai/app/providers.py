"""OpenAI-compatible provider clients.

Keys are read from environment variables only. Never put them in source,
tests, Dockerfiles, manifests, or logs.
"""
from __future__ import annotations

import json
import os
import time
import urllib.error
import urllib.request
from dataclasses import dataclass


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
            return data["choices"][0]["message"]["content"]
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


def provider_status() -> dict:
    return {
        name: {
            "configured": p.configured,
            "model": p.model,
            "base_url": p.base_url,
        }
        for name, p in PROVIDERS.items()
    }
