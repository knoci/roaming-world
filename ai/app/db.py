import os
from contextlib import contextmanager

try:
    import psycopg
except ImportError:  # agent/health still run without DB driver
    psycopg = None

from .chunking import chunk_text
from .embeddings import embed_text, vector_literal


def _dsn_parts():
    return dict(
        host=os.getenv("AI_DB_HOST", "127.0.0.1"),
        port=int(os.getenv("AI_DB_PORT", "5432")),
        user=os.getenv("AI_DB_USER", "roaming"),
        password=os.getenv("AI_DB_PASSWORD", "roaming"),
        dbname=os.getenv("AI_DB_NAME", "roaming_world"),
    )


@contextmanager
def connection():
    if psycopg is None:
        raise RuntimeError("psycopg is not installed")
    conn = psycopg.connect(**_dsn_parts())
    try:
        yield conn
        conn.commit()
    except Exception:
        conn.rollback()
        raise
    finally:
        conn.close()


def ensure_schema(conn):
    with conn.cursor() as cur:
        cur.execute("CREATE EXTENSION IF NOT EXISTS vector")
        cur.execute("""
            CREATE TABLE IF NOT EXISTS ai_documents (
                id bigserial PRIMARY KEY,
                source_type varchar(20) NOT NULL,
                source_id varchar(64) NOT NULL,
                title varchar(255) NOT NULL DEFAULT '',
                url varchar(255) NOT NULL DEFAULT '',
                content text NOT NULL,
                updated_at timestamptz NOT NULL DEFAULT now(),
                CONSTRAINT uk_ai_document UNIQUE (source_type, source_id)
            )
        """)
        cur.execute("""
            CREATE TABLE IF NOT EXISTS ai_chunks (
                id bigserial PRIMARY KEY,
                doc_id bigint NOT NULL REFERENCES ai_documents(id) ON DELETE CASCADE,
                chunk_no int NOT NULL,
                content text NOT NULL,
                embedding vector(1024) NOT NULL,
                metadata jsonb NOT NULL DEFAULT '{}'::jsonb
            )
        """)
        cur.execute("CREATE INDEX IF NOT EXISTS idx_ai_chunks_embedding ON ai_chunks USING hnsw (embedding vector_cosine_ops)")


def _documents(conn):
    docs = []
    with conn.cursor() as cur:
        cur.execute("""
            SELECT aid, title, content, '/v1/article/' || aid
            FROM articles WHERE status = 'published'
        """)
        for sid, title, content, url in cur.fetchall():
            docs.append(("article", sid, title or "", url, content or ""))
        cur.execute("SELECT sid, name, coalesce(describe,'') || E'\n' || coalesce(article,''), '/v1/scene/' || sid FROM scenes")
        for sid, title, content, url in cur.fetchall():
            docs.append(("scene", sid, title or "", url, content or ""))
        cur.execute("SELECT fid, name, coalesce(describe,'') || E'\n' || coalesce(article,''), '/v1/food/' || fid FROM foods")
        for sid, title, content, url in cur.fetchall():
            docs.append(("food", sid, title or "", url, content or ""))
        cur.execute("SELECT bid, name, coalesce(description,''), '/v1/audiobook/' || bid FROM audiobooks")
        for sid, title, content, url in cur.fetchall():
            docs.append(("audiobook", sid, title or "", url, content or ""))
    return docs


def ingest_all(conn) -> dict:
    ensure_schema(conn)
    docs = _documents(conn)
    chunks_total = 0
    with conn.cursor() as cur:
        for source_type, source_id, title, url, content in docs:
            chunks = chunk_text(content)
            if not chunks:
                continue
            cur.execute("""
                INSERT INTO ai_documents (source_type, source_id, title, url, content)
                VALUES (%s, %s, %s, %s, %s)
                ON CONFLICT (source_type, source_id)
                DO UPDATE SET title = EXCLUDED.title, url = EXCLUDED.url,
                              content = EXCLUDED.content, updated_at = now()
                RETURNING id
            """, (source_type, source_id, title, url, content))
            doc_id = cur.fetchone()[0]
            cur.execute("DELETE FROM ai_chunks WHERE doc_id = %s", (doc_id,))
            for i, chunk in enumerate(chunks):
                cur.execute("""
                    INSERT INTO ai_chunks (doc_id, chunk_no, content, embedding, metadata)
                    VALUES (%s, %s, %s, %s::vector, %s::jsonb)
                """, (doc_id, i, chunk, vector_literal(embed_text(chunk)),
                      '{"source_type":"%s","source_id":"%s"}' % (source_type, source_id)))
            chunks_total += len(chunks)
    return {"documents": len(docs), "chunks": chunks_total}


def search(conn, query: str, top_k: int = 5) -> list[dict]:
    ensure_schema(conn)
    vec = vector_literal(embed_text(query))
    with conn.cursor() as cur:
        cur.execute("""
            SELECT c.content, d.source_type, d.source_id, d.title, d.url,
                   (1 - (c.embedding <=> %s::vector))::float AS score
            FROM ai_chunks c
            JOIN ai_documents d ON d.id = c.doc_id
            ORDER BY c.embedding <=> %s::vector
            LIMIT %s
        """, (vec, vec, top_k))
        return [
            {"content": r[0], "source_type": r[1], "source_id": r[2],
             "title": r[3], "url": r[4], "score": r[5]}
            for r in cur.fetchall()
        ]
