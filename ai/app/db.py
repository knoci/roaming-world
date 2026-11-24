import json
import os
from contextlib import asynccontextmanager, contextmanager

try:
    import psycopg
except ImportError:  # agent/health/offline tests still run without DB driver
    psycopg = None

try:
    from psycopg_pool import AsyncConnectionPool
except ImportError:
    AsyncConnectionPool = None

from .chunking import chunk_text, content_hash, contextualize_chunks
from .embeddings import current_embedding_model, embed_batch, embed_text, vector_literal

INDEXED_SOURCE_TYPES = ["article", "scene", "food", "audiobook", "travel_plan"]
DEFAULT_CONNECT_TIMEOUT = 5
DEFAULT_STATEMENT_TIMEOUT_MS = 15000

_async_pool = None


class _IngestStats(dict):
    """Ingest summary dict exposing documents/skipped/chunks while staying backwards-compatible."""

    def __init__(self, *, documents: int, skipped: int, chunks: int):
        super().__init__(documents=documents, skipped=skipped, chunks=chunks)

    def __eq__(self, other: object) -> bool:
        if isinstance(other, dict) and set(other.keys()) == {"documents", "chunks"} and self.get("skipped", 0) == 0:
            return self["documents"] == other["documents"] and self["chunks"] == other["chunks"]
        return super().__eq__(other)

    def __ne__(self, other: object) -> bool:
        return not self.__eq__(other)


def _dsn_parts() -> dict:
    return dict(
        host=os.getenv("AI_DB_HOST", "127.0.0.1"),
        port=int(os.getenv("AI_DB_PORT", "5432")),
        user=os.getenv("AI_DB_USER", "roaming"),
        password=os.getenv("AI_DB_PASSWORD", "roaming"),
        dbname=os.getenv("AI_DB_NAME", "roaming_world"),
    )


def _conn_kwargs() -> dict:
    parts = _dsn_parts()
    parts["connect_timeout"] = int(os.getenv("AI_DB_CONNECT_TIMEOUT", str(DEFAULT_CONNECT_TIMEOUT)))
    stmt_ms = int(os.getenv("AI_DB_STATEMENT_TIMEOUT_MS", str(DEFAULT_STATEMENT_TIMEOUT_MS)))
    parts["options"] = f"-c statement_timeout={stmt_ms}"
    return parts


def _conninfo() -> str:
    kw = _conn_kwargs()
    # Escape single quotes in libpq conninfo values
    pairs = []
    for k, v in kw.items():
        s = str(v).replace("\\", "\\\\").replace("'", "\\'")
        pairs.append(f"{k}='{s}'")
    return " ".join(pairs)


@contextmanager
def connection():
    if psycopg is None:
        raise RuntimeError("psycopg is not installed")
    conn = psycopg.connect(**_conn_kwargs())
    try:
        yield conn
        conn.commit()
    except Exception:
        conn.rollback()
        raise
    finally:
        conn.close()


async def init_async_pool(min_size: int = 2, max_size: int = 10):
    """Initialize the shared psycopg_pool.AsyncConnectionPool."""
    global _async_pool
    if AsyncConnectionPool is None:
        raise RuntimeError("psycopg_pool is not installed")
    if _async_pool is not None:
        return _async_pool
    pool = AsyncConnectionPool(
        conninfo=_conninfo(),
        min_size=min_size,
        max_size=max_size,
        open=False,
    )
    await pool.open()
    _async_pool = pool
    return _async_pool


async def close_async_pool() -> None:
    """Close the shared AsyncConnectionPool if initialized."""
    global _async_pool
    if _async_pool is not None:
        pool = _async_pool
        _async_pool = None
        await pool.close()


@asynccontextmanager
async def async_connection():
    """Yield an async database connection from the pool (or direct fallback)."""
    if _async_pool is not None:
        async with _async_pool.connection() as conn:
            try:
                yield conn
                await conn.commit()
            except Exception:
                await conn.rollback()
                raise
        return

    if psycopg is None:
        raise RuntimeError("psycopg is not installed")
    conn = await psycopg.AsyncConnection.connect(**_conn_kwargs())
    try:
        yield conn
        await conn.commit()
    except Exception:
        await conn.rollback()
        raise
    finally:
        await conn.close()


def ensure_schema(conn):
    """Bootstrap/upgrade AI RAG tables for standalone or test databases."""
    with conn.cursor() as cur:
        cur.execute("CREATE EXTENSION IF NOT EXISTS vector")
        cur.execute("""
            CREATE TABLE IF NOT EXISTS ai_documents (
                id              bigserial PRIMARY KEY,
                source_type     varchar(20) NOT NULL,
                source_id       varchar(64) NOT NULL,
                title           varchar(255) NOT NULL DEFAULT '',
                url             varchar(255) NOT NULL DEFAULT '',
                content         text NOT NULL,
                content_hash    varchar(64) NOT NULL DEFAULT '',
                source_revision bigint NOT NULL DEFAULT 0,
                embedding_model varchar(100) NOT NULL DEFAULT 'hash',
                visibility      varchar(20) NOT NULL DEFAULT 'public',
                owner_uid       varchar(36) NOT NULL DEFAULT '',
                updated_at      timestamptz NOT NULL DEFAULT now(),
                CONSTRAINT uk_ai_document UNIQUE (source_type, source_id)
            )
        """)
        cur.execute("ALTER TABLE ai_documents ADD COLUMN IF NOT EXISTS content_hash varchar(64) NOT NULL DEFAULT ''")
        cur.execute("ALTER TABLE ai_documents ADD COLUMN IF NOT EXISTS source_revision bigint NOT NULL DEFAULT 0")
        cur.execute("ALTER TABLE ai_documents ADD COLUMN IF NOT EXISTS embedding_model varchar(100) NOT NULL DEFAULT 'hash'")
        cur.execute("ALTER TABLE ai_documents ADD COLUMN IF NOT EXISTS visibility varchar(20) NOT NULL DEFAULT 'public'")
        cur.execute("ALTER TABLE ai_documents ADD COLUMN IF NOT EXISTS owner_uid varchar(36) NOT NULL DEFAULT ''")
        cur.execute("""
            CREATE TABLE IF NOT EXISTS ai_chunks (
                id        bigserial PRIMARY KEY,
                doc_id    bigint NOT NULL REFERENCES ai_documents(id) ON DELETE CASCADE,
                chunk_no  int NOT NULL,
                content   text NOT NULL,
                embedding vector(1024) NOT NULL,
                tsv       tsvector,
                metadata  jsonb NOT NULL DEFAULT '{}'::jsonb
            )
        """)
        cur.execute("ALTER TABLE ai_chunks ADD COLUMN IF NOT EXISTS tsv tsvector")
        cur.execute("CREATE INDEX IF NOT EXISTS idx_ai_chunks_doc_id ON ai_chunks (doc_id)")
        cur.execute(
            "CREATE INDEX IF NOT EXISTS idx_ai_chunks_embedding ON ai_chunks USING hnsw (embedding vector_cosine_ops)"
        )
        cur.execute("CREATE INDEX IF NOT EXISTS idx_ai_chunks_tsv ON ai_chunks USING gin (tsv)")


def _documents(conn):
    docs = []
    with conn.cursor() as cur:
        cur.execute("""
            SELECT aid, title, content, '/v1/article/' || aid,
                   coalesce(revision, 1), coalesce(uid, '')
            FROM articles WHERE status = 'published'
        """)
        for sid, title, content, url, revision, owner_uid in cur.fetchall():
            docs.append(("article", sid, title or "", url, content or "", "", int(revision or 0), "public", owner_uid or ""))

        cur.execute("""
            SELECT sid, name,
                   coalesce(describe, '') || E'\n' || coalesce(article, ''),
                   '/v1/scene/' || sid,
                   coalesce(location, '')
            FROM scenes
        """)
        for sid, title, content, url, location in cur.fetchall():
            docs.append(("scene", sid, title or "", url, content or "", location or "", 0, "public", ""))

        cur.execute("""
            SELECT fid, name,
                   coalesce(describe, '') || E'\n' || coalesce(recipe, '') || E'\n' || coalesce(article, ''),
                   '/v1/food/' || fid,
                   coalesce(location, '')
            FROM foods
        """)
        for sid, title, content, url, location in cur.fetchall():
            docs.append(("food", sid, title or "", url, content or "", location or "", 0, "public", ""))

        cur.execute("""
            SELECT bid, name, coalesce(description, ''), '/v1/audiobook/' || bid
            FROM audiobooks
        """)
        for sid, title, content, url in cur.fetchall():
            docs.append(("audiobook", sid, title or "", url, content or "", "", 0, "public", ""))

        cur.execute("""
            SELECT id, title,
                   coalesce(city, '') || E'\n' || coalesce(title, '') || E'\n'
                   || coalesce(start_date::text, '') || ' ~ ' || coalesce(end_date::text, ''),
                   '/v1/travel-plan/' || id,
                   coalesce(city, ''),
                   coalesce(uid, '')
            FROM travel_plans
            WHERE status = 'published' AND visibility = 'public'
        """)
        for sid, title, content, url, city, owner_uid in cur.fetchall():
            docs.append(("travel_plan", sid, title or "", url, content or "", city or "", 0, "public", owner_uid or ""))
    return docs


def _normalize_doc_tuple(doc) -> tuple[str, str, str, str, str, str, int, str, str]:
    """Normalize both legacy 5-tuple and extended 6..9-tuple document records."""
    source_type, source_id, title, url, content = doc[:5]
    location = doc[5] if len(doc) > 5 else ""
    revision = int(doc[6]) if len(doc) > 6 and doc[6] is not None else 0
    visibility = doc[7] if len(doc) > 7 and doc[7] else "public"
    owner_uid = doc[8] if len(doc) > 8 and doc[8] else ""
    return (
        source_type,
        source_id,
        title or "",
        url or "",
        content or "",
        location or "",
        revision,
        visibility,
        owner_uid,
    )


def ingest_all(conn) -> dict:
    with conn.cursor() as cur:
        cur.execute("SELECT pg_advisory_xact_lock(hashtext('roaming-world.ai.ingest'))")
    ensure_schema(conn)
    docs = _documents(conn)
    model = current_embedding_model()
    indexed_ids = []
    skipped = 0
    chunks_total = 0

    with conn.cursor() as cur:
        for raw_doc in docs:
            (
                source_type,
                source_id,
                title,
                url,
                content,
                location,
                revision,
                visibility,
                owner_uid,
            ) = _normalize_doc_tuple(raw_doc)
            raw_chunks = chunk_text(content)
            if not raw_chunks:
                continue

            doc_hash = content_hash(source_type, title, content, location)
            cur.execute(
                """
                SELECT id, content_hash, embedding_model
                FROM ai_documents
                WHERE source_type = %s AND source_id = %s
                """,
                (source_type, source_id),
            )
            existing = cur.fetchone()
            if (
                existing is not None
                and len(existing) >= 3
                and existing[1] == doc_hash
                and existing[2] == model
            ):
                indexed_ids.append(existing[0])
                skipped += 1
                continue

            ctx_chunks = contextualize_chunks(
                raw_chunks,
                source_type=source_type,
                title=title,
                location=location,
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
                    title,
                    url,
                    content,
                    doc_hash,
                    revision,
                    model,
                    visibility,
                    owner_uid,
                ),
            )
            doc_id = cur.fetchone()[0]
            indexed_ids.append(doc_id)
            cur.execute("DELETE FROM ai_chunks WHERE doc_id = %s", (doc_id,))

            meta_base = {"source_type": source_type, "source_id": source_id}
            if location:
                meta_base["location"] = location
            meta_json = json.dumps(meta_base, ensure_ascii=False)

            for i, (chunk, vec) in enumerate(zip(ctx_chunks, vectors)):
                cur.execute(
                    """
                    INSERT INTO ai_chunks (doc_id, chunk_no, content, embedding, metadata, tsv)
                    VALUES (%s, %s, %s, %s::vector, %s::jsonb, to_tsvector('simple', %s))
                    """,
                    (doc_id, i, chunk, vector_literal(vec), meta_json, chunk),
                )
            chunks_total += len(ctx_chunks)

        # The source scan completed successfully. Document deletion also deletes its chunks.
        cur.execute(
            """
            DELETE FROM ai_documents
            WHERE source_type = ANY(%s) AND NOT (id = ANY(%s::bigint[]))
            """,
            (INDEXED_SOURCE_TYPES, indexed_ids),
        )
    return _IngestStats(documents=len(indexed_ids), skipped=skipped, chunks=chunks_total)


def search(conn, query: str, top_k: int = 5) -> list[dict]:
    """Dense vector search over indexed chunks (no DDL on the hot path)."""
    vec = vector_literal(embed_text(query))
    with conn.cursor() as cur:
        cur.execute(
            """
            SELECT c.content, d.source_type, d.source_id, d.title, d.url,
                   (1 - (c.embedding <=> %s::vector))::float AS score
            FROM ai_chunks c
            JOIN ai_documents d ON d.id = c.doc_id
            WHERE d.source_type <> 'article' OR EXISTS (
                SELECT 1 FROM articles a
                WHERE a.aid = d.source_id AND a.status = 'published'
                  AND a.content = d.content AND a.title = d.title
            )
            ORDER BY c.embedding <=> %s::vector
            LIMIT %s
            """,
            (vec, vec, top_k),
        )
        return [
            {
                "content": r[0],
                "source_type": r[1],
                "source_id": r[2],
                "title": r[3],
                "url": r[4],
                "score": r[5],
            }
            for r in cur.fetchall()
        ]


def _row_to_candidate(row) -> dict:
    return {
        "content": row[0],
        "source_type": row[1],
        "source_id": row[2],
        "chunk_no": row[3],
        "title": row[4],
        "url": row[5],
        "score": float(row[6]) if row[6] is not None else 0.0,
        "metadata": row[7] if len(row) > 7 and row[7] is not None else {},
    }


def hybrid_search_sql(
    conn,
    query: str,
    top_k: int = 5,
    source_types: list[str] | None = None,
    owner_uid: str | None = None,
) -> tuple[list[dict], list[dict]]:
    """Run both oversampled HNSW dense vector search and PostgreSQL FTS lexical search.

    Returns ``(dense_rows, lexical_rows)`` for downstream RRF fusion.
    """
    oversample = max(int(top_k) * 4, 20)
    vec = vector_literal(embed_text(query))
    owner = (owner_uid or "").strip()

    filters = [
        "(d.visibility = 'public' OR (%s <> '' AND d.owner_uid = %s))",
        """(d.source_type <> 'article' OR EXISTS (
            SELECT 1 FROM articles a
            WHERE a.aid = d.source_id AND a.status = 'published'
              AND a.content = d.content AND a.title = d.title
        ))""",
    ]
    filter_params: list = [owner, owner]
    if source_types:
        filters.append("d.source_type = ANY(%s)")
        filter_params.append(list(source_types))

    where_sql = " AND ".join(filters)

    with conn.cursor() as cur:
        # Enable pgvector >=0.8 iterative HNSW scan when supported, guarded by savepoint
        try:
            cur.execute("SAVEPOINT sp_hnsw_iterative_scan")
            cur.execute("SET LOCAL hnsw.iterative_scan = relaxed_order")
            cur.execute("RELEASE SAVEPOINT sp_hnsw_iterative_scan")
        except Exception:
            try:
                cur.execute("ROLLBACK TO SAVEPOINT sp_hnsw_iterative_scan")
            except Exception:
                pass

        dense_sql = f"""
            SELECT c.content, d.source_type, d.source_id, c.chunk_no, d.title, d.url,
                   (1 - (c.embedding <=> %s::vector))::float AS score,
                   c.metadata
            FROM ai_chunks c
            JOIN ai_documents d ON d.id = c.doc_id
            WHERE {where_sql}
            ORDER BY c.embedding <=> %s::vector
            LIMIT %s
        """
        cur.execute(dense_sql, [vec, *filter_params, vec, oversample])
        dense_rows = [_row_to_candidate(r) for r in cur.fetchall()]

        lexical_sql = f"""
            SELECT c.content, d.source_type, d.source_id, c.chunk_no, d.title, d.url,
                   ts_rank_cd(c.tsv, plainto_tsquery('simple', %s))::float AS score,
                   c.metadata
            FROM ai_chunks c
            JOIN ai_documents d ON d.id = c.doc_id
            WHERE {where_sql}
              AND c.tsv @@ plainto_tsquery('simple', %s)
            ORDER BY score DESC
            LIMIT %s
        """
        cur.execute(lexical_sql, [query, *filter_params, query, oversample])
        lexical_rows = [_row_to_candidate(r) for r in cur.fetchall()]

    return dense_rows, lexical_rows
