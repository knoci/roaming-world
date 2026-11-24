-- Phase 2 schema updates, high-impact indexes, and AI Hybrid RAG tables.
-- Fully idempotent: safe to re-run on both fresh and upgraded databases.

BEGIN;

CREATE EXTENSION IF NOT EXISTS vector;

-- 1. User token version for refresh-token / session revocation.
ALTER TABLE users ADD COLUMN IF NOT EXISTS token_version bigint NOT NULL DEFAULT 1;

-- 2. Video article sequence and partial unique index.
CREATE SEQUENCE IF NOT EXISTS article_videoid_seq;

DO $$
DECLARE
    max_vid bigint;
    seq_val bigint;
    seq_called boolean;
BEGIN
    SELECT COALESCE(MAX(videoid), 0) INTO max_vid FROM articles WHERE video = true;
    SELECT last_value, is_called INTO seq_val, seq_called FROM article_videoid_seq;
    IF max_vid > 0 THEN
        IF (NOT seq_called) OR seq_val < max_vid THEN
            PERFORM setval('article_videoid_seq', GREATEST(max_vid, 1), true);
        END IF;
    ELSIF (NOT seq_called) AND seq_val <> 1 THEN
        PERFORM setval('article_videoid_seq', 1, false);
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS uk_articles_videoid ON articles (videoid) WHERE video = true AND videoid > 0;

-- 3. High-impact indexes missing from 001_init.sql.
CREATE INDEX IF NOT EXISTS idx_likes_aid ON likes (aid);
CREATE INDEX IF NOT EXISTS idx_favorites_aid ON favorites (aid);
CREATE INDEX IF NOT EXISTS idx_commentlikes_cid ON commentlikes (cid);

CREATE INDEX IF NOT EXISTS idx_comments_target_status ON comments (target, status);
CREATE INDEX IF NOT EXISTS idx_comments_active_roots ON comments (target, created_at DESC) WHERE status = 'active' AND (replycid = '' OR replycid IS NULL);
CREATE INDEX IF NOT EXISTS idx_comments_active_replies ON comments (replycid, created_at DESC) WHERE status = 'active' AND replycid <> '';

CREATE INDEX IF NOT EXISTS idx_articles_pub_created ON articles (created_at DESC, aid DESC) WHERE status = 'published';
CREATE INDEX IF NOT EXISTS idx_articles_pub_uid_created ON articles (uid, created_at DESC) WHERE status = 'published';
CREATE INDEX IF NOT EXISTS idx_articles_pub_likes ON articles (likes DESC, aid DESC) WHERE status = 'published';
CREATE INDEX IF NOT EXISTS idx_articles_pub_favorite ON articles (favorite DESC, aid DESC) WHERE status = 'published';

CREATE INDEX IF NOT EXISTS idx_travel_plans_pub_updated ON travel_plans (status, visibility, updated_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_travel_plans_uid_updated ON travel_plans (uid, updated_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_follows_followee_created ON follows (followee_uid, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_follows_follower_created ON follows (follower_uid, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_notifications_uid_created ON notifications (uid, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_uid_unread ON notifications (uid, created_at DESC) WHERE read_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_comment_outbox_pending_partial ON comment_outbox (next_retry_at, id) WHERE status IN ('pending', 'processing');

CREATE INDEX IF NOT EXISTS idx_scenes_location ON scenes (location);
CREATE INDEX IF NOT EXISTS idx_foods_location ON foods (location);

-- 4. AI RAG tables (ready for Phase 3 Hybrid RAG).
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
);
ALTER TABLE ai_documents ADD COLUMN IF NOT EXISTS content_hash varchar(64) NOT NULL DEFAULT '';
ALTER TABLE ai_documents ADD COLUMN IF NOT EXISTS source_revision bigint NOT NULL DEFAULT 0;
ALTER TABLE ai_documents ADD COLUMN IF NOT EXISTS embedding_model varchar(100) NOT NULL DEFAULT 'hash';
ALTER TABLE ai_documents ADD COLUMN IF NOT EXISTS visibility varchar(20) NOT NULL DEFAULT 'public';
ALTER TABLE ai_documents ADD COLUMN IF NOT EXISTS owner_uid varchar(36) NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS ai_chunks (
    id        bigserial PRIMARY KEY,
    doc_id    bigint NOT NULL REFERENCES ai_documents(id) ON DELETE CASCADE,
    chunk_no  int NOT NULL,
    content   text NOT NULL,
    embedding vector(1024) NOT NULL,
    tsv       tsvector,
    metadata  jsonb NOT NULL DEFAULT '{}'::jsonb
);
ALTER TABLE ai_chunks ADD COLUMN IF NOT EXISTS tsv tsvector;
CREATE INDEX IF NOT EXISTS idx_ai_chunks_doc_id ON ai_chunks (doc_id);
CREATE INDEX IF NOT EXISTS idx_ai_chunks_embedding ON ai_chunks USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);
CREATE INDEX IF NOT EXISTS idx_ai_chunks_tsv ON ai_chunks USING gin (tsv);

COMMIT;
