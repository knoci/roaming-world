"""Optional OpenAI-compatible chat generation.

Default is extractive: no API key is required. Configure:
  LLM_PROVIDER=openai
  LLM_API_URL=https://api.openai.com/v1/chat/completions
  LLM_API_KEY=...
  LLM_MODEL=...
"""
from __future__ import annotations

import json
import os
import urllib.request


def build_context(answer: str, citations: list) -> str:
    lines = [answer]
    for c in citations[:5]:
        lines.append(f"[{c.source_type}:{c.source_id}] {c.title}")
    return "\n".join(lines)


def generate_answer(query: str, context: str) -> str | None:
    if os.getenv("LLM_PROVIDER", "extractive").lower() != "openai":
        return None
    key = os.getenv("LLM_API_KEY", "")
    if not key:
        return None
    url = os.getenv("LLM_API_URL", "https://api.openai.com/v1/chat/completions")
    model = os.getenv("LLM_MODEL", "gpt-4o-mini")
    body = {
        "model": model,
        "temperature": 0.2,
        "messages": [
            {"role": "system", "content": "你是旅游内容助手。只能根据给定资料回答，保留来源方括号引用；资料不足就说不知道。"},
            {"role": "user", "content": f"问题：{query}\n\n资料：\n{context}"},
        ],
    }
    req = urllib.request.Request(
        url, data=json.dumps(body).encode("utf-8"),
        headers={"Content-Type": "application/json", "Authorization": f"Bearer {key}"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=45) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        return data["choices"][0]["message"]["content"]
    except Exception:
        return None
