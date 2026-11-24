from dataclasses import dataclass
import json

from .chunking import chunk_text, content_hash, contextualize_chunks
from .embeddings import current_embedding_model, embed_batch, vector_literal

_DELETE_EVENTS = {"delete", "deleted", "unpublish", "unpublished", "offline", "remove", "removed"}
_UPSERT_EVENTS = {"upsert", "create", "created", "update", "updated", "publish", "published"}
_HIDDEN_VISIBILITIES = {"deleted", "offline", "draft", "unpublished"}


@dataclass
class IndexEvent:
    event_type: str  # "upsert" | "delete"
    source_type: str
    source_id: str
    title: str = ""
    content: str = ""
    location: str = ""
    url: str = ""
    revision: int = 0
    visibility: str = "public"
    owner_uid: str = ""


def parse_kafka_message(raw: bytes | str) -> IndexEvent | None:
    """Parse a raw Kafka JSON payload into an ``IndexEvent``, or ``None`` if invalid."""
    if raw is None:
        return None
    try:
        text = raw.decode("utf-8") if isinstance(raw, (bytes, bytearray)) else str(raw)
        payload = json.loads(text)
    except (UnicodeDecodeError, json.JSONDecodeError, TypeError):
        return None

    if not isinstance(payload, dict):
        return None

    raw_event = str(payload.get("event_type") or payload.get("action") or payload.get("type") or "").strip().lower()
    if raw_event in _DELETE_EVENTS:
        event_type = "delete"
    elif raw_event in _UPSERT_EVENTS:
        event_type = "upsert"
    else:
        return None

    source_type = str(payload.get("source_type") or "").strip()
    source_id = str(payload.get("source_id") or "").strip()
    if not source_type or not source_id:
        return None

    status = str(payload.get("status") or "").strip().lower()
    visibility = str(payload.get("visibility") or "public").strip().lower() or "public"
    if status in _HIDDEN_VISIBILITIES:
        event_type = "delete"

    try:
        revision = int(payload.get("revision") or 0)
    except (TypeError, ValueError):
        revision = 0

    return IndexEvent(
        event_type=event_type,
        source_type=source_type,
        source_id=source_id,
        title=str(payload.get("title") or ""),
        content=str(payload.get("content") or ""),
        location=str(payload.get("location") or ""),
        url=str(payload.get("url") or ""),
        revision=revision,
        visibility=visibility,
        owner_uid=str(payload.get("owner_uid") or ""),
    )


class IncrementalIndexer:
    """Idempotent event-driven document indexer for real-time RAG synchronization."""

    parse_kafka_message = staticmethod(parse_kafka_message)

    def apply_event(self, conn, event: IndexEvent) -> dict:
        source_type = (event.source_type or "").strip()
        source_id = (event.source_id or "").strip()
        if not source_type or not source_id:
            raise ValueError("IndexEvent requires non-empty source_type and source_id")

        visibility = (event.visibility or "public").strip().lower()
        should_delete = (
            (event.event_type or "").strip().lower() in _DELETE_EVENTS
            or visibility in _HIDDEN_VISIBILITIES
            or not (event.content or "").strip()
        )

        if should_delete:
            with conn.cursor() as cur:
                cur.execute(
                    "DELETE FROM ai_documents WHERE source_type = %s AND source_id = %s",
                    (source_type, source_id),
                )
            return {
                "action": "deleted",
                "source_type": source_type,
                "source_id": source_id,
                "chunks": 0,
            }

        doc_hash = content_hash(source_type, event.title, event.content, event.location)
        model = current_embedding_model()

        with conn.cursor() as cur:
            cur.execute(
                """
                SELECT id, content_hash, embedding_model, source_revision
                FROM ai_documents
                WHERE source_type = %s AND source_id = %s
                """,
                (source_type, source_id),
            )
            existing = cur.fetchone()
            if existing is not None and len(existing) >= 3:
                existing_hash = existing[1]
                existing_model = existing[2]
                existing_rev = int(existing[3]) if len(existing) >= 4 and existing[3] is not None else 0
                if event.revision > 0 and existing_rev > event.revision:
                    return {
                        "action": "stale_ignored",
                        "source_type": source_type,
                        "source_id": source_id,
                        "chunks": 0,
                    }
                if existing_hash == doc_hash and existing_model == model:
                    return {
                        "action": "skipped",
                        "source_type": source_type,
                        "source_id": source_id,
                        "chunks": 0,
                    }

            raw_chunks = chunk_text(event.content)
            ctx_chunks = contextualize_chunks(
                raw_chunks,
                source_type=source_type,
                title=event.title,
                location=event.location,
            )
            vectors = embed_batch(ctx_chunks)

            cur.execute(
                """
                INSERT INTO ai_documents (
                    source_type, source_id, title, url, content,
                    content_hash, source_revision, embedding_model, visibility, owner_uid
                )
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
                ON CONFLICT (source_type, source_id)
                DO UPDATE SET title = EXCLUDED.title,
                              url = EXCLUDED.url,
                              content = EXCLUDED.content,
                              content_hash = EXCLUDED.content_hash,
                              source_revision = EXCLUDED.source_revision,
                              embedding_model = EXCLUDED.embedding_model,
                              visibility = EXCLUDED.visibility,
                              owner_uid = EXCLUDED.owner_uid,
                              updated_at = now()
                RETURNING id
                """,
                (
                    source_type,
                    source_id,
                    event.title,
                    event.url,
                    event.content,
                    doc_hash,
                    int(event.revision or 0),
                    model,
                    visibility,
                    event.owner_uid,
                ),
            )
            doc_id = cur.fetchone()[0]
            cur.execute("DELETE FROM ai_chunks WHERE doc_id = %s", (doc_id,))

            meta = {"source_type": source_type, "source_id": source_id}
            if event.location:
                meta["location"] = event.location
            meta_json = json.dumps(meta, ensure_ascii=False)

            for i, (chunk, vec) in enumerate(zip(ctx_chunks, vectors)):
                cur.execute(
                    """
                    INSERT INTO ai_chunks (doc_id, chunk_no, content, embedding, metadata, tsv)
                    VALUES (%s, %s, %s, %s::vector, %s::jsonb, to_tsvector('simple', %s))
                    """,
                    (doc_id, i, chunk, vector_literal(vec), meta_json, chunk),
                )

        return {
            "action": "upserted",
            "source_type": source_type,
            "source_id": source_id,
            "chunks": len(ctx_chunks),
        }
