import json
import logging
import os
import re
import urllib.request
from typing import Callable

from .db import hybrid_search_sql

logger = logging.getLogger("ai-engine.rag")


def _candidate_key(row: dict) -> tuple[str, str, object]:
    source_type = str(row.get("source_type", ""))
    source_id = str(row.get("source_id", ""))
    if "chunk_no" in row and row["chunk_no"] is not None:
        return (source_type, source_id, row["chunk_no"])
    return (source_type, source_id, str(row.get("content", "")))


def _doc_key(row: dict) -> tuple[str, str]:
    return (str(row.get("source_type", "")), str(row.get("source_id", "")))


def _tokenize(text: str) -> set[str]:
    text = (text or "").lower()
    words = re.findall(r"[a-z0-9_]+", text)
    chars = [c for c in text if "\u4e00" <= c <= "\u9fff"]
    bigrams = ["".join(chars[i:i + 2]) for i in range(len(chars) - 1)]
    return set(words + chars + bigrams)


def rrf_fuse(
    dense_rows: list[dict],
    lexical_rows: list[dict],
    k: int = 60,
) -> list[dict]:
    """Reciprocal Rank Fusion merging dense and lexical ranked lists."""
    k = max(1, int(k))
    merged: dict[tuple[str, str, object], dict] = {}

    for rank, row in enumerate(dense_rows or [], start=1):
        key = _candidate_key(row)
        entry = merged.get(key)
        if entry is None:
            entry = dict(row)
            entry["rrf_score"] = 0.0
            merged[key] = entry
        entry["dense_rank"] = rank
        entry["dense_score"] = float(row.get("score", 0.0))
        entry["rrf_score"] += 1.0 / (k + rank)

    for rank, row in enumerate(lexical_rows or [], start=1):
        key = _candidate_key(row)
        entry = merged.get(key)
        if entry is None:
            entry = dict(row)
            entry["rrf_score"] = 0.0
            merged[key] = entry
        entry["lexical_rank"] = rank
        entry["lexical_score"] = float(row.get("score", 0.0))
        entry["rrf_score"] += 1.0 / (k + rank)

    fused = list(merged.values())
    for item in fused:
        item["score"] = item["rrf_score"]

    fused.sort(
        key=lambda r: (
            -float(r["rrf_score"]),
            str(r.get("source_type", "")),
            str(r.get("source_id", "")),
            str(r.get("chunk_no", "")),
        )
    )
    return fused


def _external_rerank(query: str, candidates: list[dict], top_n: int) -> list[dict] | None:
    url = os.getenv("AI_RERANK_API_URL", "").strip()
    if not url:
        return None
    key = os.getenv("AI_RERANK_API_KEY", "").strip()
    model = os.getenv("AI_RERANK_MODEL", "BAAI/bge-reranker-v2-m3").strip()
    headers = {"Content-Type": "application/json"}
    if key:
        headers["Authorization"] = f"Bearer {key}"
    payload = {
        "model": model,
        "query": query,
        "documents": [str(c.get("content", "")) for c in candidates],
        "top_n": min(len(candidates), max(1, top_n)),
    }
    req = urllib.request.Request(
        url,
        data=json.dumps(payload, ensure_ascii=False).encode("utf-8"),
        headers=headers,
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        results = data.get("results") or data.get("data") or []
        scored: list[dict] = []
        for item in results:
            idx = int(item["index"])
            if 0 <= idx < len(candidates):
                row = dict(candidates[idx])
                row["rerank_score"] = float(item.get("relevance_score", item.get("score", 0.0)))
                row["score"] = row["rerank_score"]
                scored.append(row)
        return scored
    except Exception as exc:
        logger.warning("external rerank failed, falling back to local reranker: %s", exc)
        return None


def rerank_candidates(
    query: str,
    candidates: list[dict],
    top_k: int = 5,
    min_score: float = 0.01,
    max_per_doc: int = 2,
) -> list[dict]:
    """Rerank candidates with optional external API or deterministic lexical+title+RRF scorer.

    Enforces document-level diversity deduplication (at most ``max_per_doc`` chunks per
    ``(source_type, source_id)``) and drops candidates below ``min_score`` so callers can
    honestly abstain when evidence is insufficient.
    """
    if not candidates or top_k <= 0:
        return []

    max_per_doc = max(1, int(max_per_doc))
    scored = _external_rerank(query, candidates, top_n=len(candidates))

    if scored is None:
        q_tokens = _tokenize(query)
        scored = []
        for cand in candidates:
            row = dict(cand)
            base = float(row.get("rrf_score", row.get("score", 0.0)))
            if q_tokens:
                content_tokens = _tokenize(str(row.get("content", "")))
                title_tokens = _tokenize(str(row.get("title", "")))
                content_cov = len(q_tokens & content_tokens) / len(q_tokens)
                title_cov = len(q_tokens & title_tokens) / len(q_tokens)
                if content_cov > 0.0 or title_cov > 0.0:
                    final_score = base + 0.35 * content_cov + 0.25 * title_cov
                else:
                    final_score = base
            else:
                final_score = base
            row["rerank_score"] = final_score
            row["score"] = final_score
            scored.append(row)

    scored.sort(
        key=lambda r: (
            -float(r.get("rerank_score", r.get("score", 0.0))),
            str(r.get("source_type", "")),
            str(r.get("source_id", "")),
            str(r.get("chunk_no", "")),
        )
    )

    selected: list[dict] = []
    per_doc_counts: dict[tuple[str, str], int] = {}
    for row in scored:
        if float(row.get("rerank_score", row.get("score", 0.0))) < min_score:
            continue
        dk = _doc_key(row)
        count = per_doc_counts.get(dk, 0)
        if count >= max_per_doc:
            continue
        per_doc_counts[dk] = count + 1
        selected.append(row)
        if len(selected) >= top_k:
            break

    return selected


class HybridRetriever:
    """Hybrid dense + lexical retriever with RRF fusion and reranking."""

    def __init__(
        self,
        *,
        vector_fn: Callable[[str, int], list[dict]] | None = None,
        lexical_fn: Callable[[str, int], list[dict]] | None = None,
        conn=None,
        rrf_k: int = 60,
        min_score: float = 0.01,
        max_per_doc: int = 2,
    ):
        self.vector_fn = vector_fn
        self.lexical_fn = lexical_fn
        self.conn = conn
        self.rrf_k = rrf_k
        self.min_score = min_score
        self.max_per_doc = max_per_doc

    def retrieve(
        self,
        query: str,
        top_k: int = 5,
        *,
        source_types: list[str] | None = None,
        owner_uid: str | None = None,
    ) -> list[dict]:
        oversample = max(int(top_k) * 4, 20)
        if self.vector_fn is not None or self.lexical_fn is not None:
            dense_rows = self.vector_fn(query, oversample) if self.vector_fn is not None else []
            lexical_rows = self.lexical_fn(query, oversample) if self.lexical_fn is not None else []
        elif self.conn is not None:
            dense_rows, lexical_rows = hybrid_search_sql(
                self.conn,
                query,
                top_k=top_k,
                source_types=source_types,
                owner_uid=owner_uid,
            )
        else:
            raise ValueError("HybridRetriever requires vector_fn/lexical_fn or a DB connection")

        fused = rrf_fuse(dense_rows, lexical_rows, k=self.rrf_k)
        return rerank_candidates(
            query,
            fused,
            top_k=top_k,
            min_score=self.min_score,
            max_per_doc=self.max_per_doc,
        )
