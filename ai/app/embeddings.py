import hashlib
import json
import math
import os
import re
import urllib.request

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


def embed_text(text: str) -> list[float]:
    """Use an OpenAI-compatible embedding API when configured, else hash fallback."""
    provider = os.getenv("EMBEDDING_PROVIDER", "hash").lower()
    if provider == "openai":
        url = os.getenv("EMBEDDING_API_URL", "https://api.openai.com/v1/embeddings")
        key = os.getenv("EMBEDDING_API_KEY", "")
        model = os.getenv("EMBEDDING_MODEL", "BAAI/bge-m3")
        if not key:
            raise RuntimeError("EMBEDDING_API_KEY is required for openai provider")
        req = urllib.request.Request(
            url,
            data=json.dumps({"model": model, "input": text}).encode("utf-8"),
            headers={"Content-Type": "application/json", "Authorization": f"Bearer {key}"},
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=30) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        vec = data["data"][0]["embedding"]
        if len(vec) != DIM:
            raise RuntimeError(f"embedding dim {len(vec)} != {DIM}")
        return vec
    return hash_embed(text)


def vector_literal(vec: list[float]) -> str:
    return "[" + ",".join(f"{v:.6f}" for v in vec) + "]"
