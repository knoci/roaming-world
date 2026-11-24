import asyncio
import hashlib
import json
import math
import os
import re
import urllib.request

try:
    import httpx
except ImportError:
    httpx = None

DIM = 1024


def _tokens(text: str) -> list[str]:
    text = (text or "").lower()
    words = re.findall(r"[a-z0-9_]+", text)
    chars = [c for c in text if "\u4e00" <= c <= "\u9fff"]
    bigrams = ["".join(chars[i:i + 2]) for i in range(len(chars) - 1)]
    return words + chars + bigrams


def hash_embed(text: str, dim: int = DIM) -> list[float]:
    """Offline deterministic embedding for local/dev RAG.

    This is not a semantic model. Production should use BGE-M3 or another
    embedding service; the hash provider only keeps the pipeline runnable
    without an API key.
    """
    vec = [0.0] * dim
    for token in _tokens(text):
        h = hashlib.sha256(token.encode("utf-8")).digest()
        idx = int.from_bytes(h[:4], "big") % dim
        sign = 1.0 if h[4] & 1 else -1.0
        vec[idx] += sign
    norm = math.sqrt(sum(v * v for v in vec))
    if norm == 0:
        return vec
    return [v / norm for v in vec]


def _env_first(*names: str, default: str = "") -> str:
    for name in names:
        val = os.getenv(name)
        if val is not None and val.strip() != "":
            return val.strip()
    return default


def _provider() -> str:
    return _env_first("AI_EMBEDDING_PROVIDER", "EMBEDDING_PROVIDER", default="hash").lower()


def current_embedding_model() -> str:
    """Return the active embedding model identifier used for index change tracking."""
    if _provider() == "openai":
        return _env_first("AI_EMBEDDING_MODEL", "EMBEDDING_MODEL", default="BAAI/bge-m3")
    return "hash"


def _openai_config() -> tuple[str, str, str]:
    url = _env_first("AI_EMBEDDING_API_URL", "EMBEDDING_API_URL", default="https://api.openai.com/v1/embeddings")
    key = _env_first("AI_EMBEDDING_API_KEY", "EMBEDDING_API_KEY", default="")
    model = _env_first("AI_EMBEDDING_MODEL", "EMBEDDING_MODEL", default="BAAI/bge-m3")
    if not key:
        raise RuntimeError("AI_EMBEDDING_API_KEY or EMBEDDING_API_KEY is required for openai provider")
    return url, key, model


def _parse_embedding_response(data: dict, expected_count: int) -> list[list[float]]:
    items = data.get("data") or []
    if len(items) != expected_count:
        raise RuntimeError(f"embedding count {len(items)} != {expected_count}")
    ordered = sorted(items, key=lambda item: item.get("index", 0))
    vecs: list[list[float]] = []
    for item in ordered:
        vec = item["embedding"]
        if len(vec) != DIM:
            raise RuntimeError(f"embedding dim {len(vec)} != {DIM}")
        vecs.append(vec)
    return vecs


def embed_text(text: str) -> list[float]:
    """Use an OpenAI-compatible embedding API when configured, else hash fallback."""
    if _provider() == "openai":
        url, key, model = _openai_config()
        req = urllib.request.Request(
            url,
            data=json.dumps({"model": model, "input": text}).encode("utf-8"),
            headers={"Content-Type": "application/json", "Authorization": f"Bearer {key}"},
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=30) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        return _parse_embedding_response(data, 1)[0]
    return hash_embed(text)


def embed_batch(texts: list[str], batch_size: int = 32) -> list[list[float]]:
    """Synchronous batched embedding supporting OpenAI-compatible APIs and hash fallback."""
    if not texts:
        return []
    if _provider() != "openai":
        return [hash_embed(t) for t in texts]

    url, key, model = _openai_config()
    size = max(1, int(batch_size))
    results: list[list[float]] = []
    for start in range(0, len(texts), size):
        batch = texts[start:start + size]
        req = urllib.request.Request(
            url,
            data=json.dumps({"model": model, "input": batch}).encode("utf-8"),
            headers={"Content-Type": "application/json", "Authorization": f"Bearer {key}"},
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=30) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        results.extend(_parse_embedding_response(data, len(batch)))
    return results


async def aembed_batch(texts: list[str], batch_size: int = 32) -> list[list[float]]:
    """Async batched embedding using httpx.AsyncClient when available."""
    if not texts:
        return []
    if _provider() != "openai":
        return [hash_embed(t) for t in texts]

    if httpx is None:
        return await asyncio.to_thread(embed_batch, texts, batch_size)

    url, key, model = _openai_config()
    size = max(1, int(batch_size))
    headers = {"Content-Type": "application/json", "Authorization": f"Bearer {key}"}
    results: list[list[float]] = []
    async with httpx.AsyncClient(timeout=30.0) as client:
        for start in range(0, len(texts), size):
            batch = texts[start:start + size]
            resp = await client.post(url, json={"model": model, "input": batch}, headers=headers)
            resp.raise_for_status()
            results.extend(_parse_embedding_response(resp.json(), len(batch)))
    return results


def vector_literal(vec: list[float]) -> str:
    return "[" + ",".join(f"{v:.6f}" for v in vec) + "]"
