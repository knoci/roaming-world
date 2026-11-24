-- Reconcile denormalized counters with source tables in bounded batches.
-- Run by K8s CronJob or manually: psql -v ON_ERROR_STOP=1 -f reconcile_counts.sql
-- Do NOT wrap this file in BEGIN/COMMIT: CALL commits each batch to release row locks.

SET lock_timeout = '3s';
SET statement_timeout = '30s';

CREATE OR REPLACE PROCEDURE reconcile_counts_batched(batch_size int DEFAULT 500)
LANGUAGE plpgsql
AS $$
DECLARE
    last_aid  varchar(36);
    last_cid  varchar(36);
    batch_ids varchar(36)[];
BEGIN
    IF batch_size IS NULL OR batch_size <= 0 THEN
        batch_size := 500;
    END IF;

    -- Reconcile articles (likes, favorite, active comments) in keyset batches.
    last_aid := NULL;
    LOOP
        IF last_aid IS NULL THEN
            SELECT array_agg(aid ORDER BY aid)
            INTO batch_ids
            FROM (
                SELECT aid
                FROM articles
                ORDER BY aid
                LIMIT batch_size
                FOR NO KEY UPDATE
            ) s;
        ELSE
            SELECT array_agg(aid ORDER BY aid)
            INTO batch_ids
            FROM (
                SELECT aid
                FROM articles
                WHERE aid > last_aid
                ORDER BY aid
                LIMIT batch_size
                FOR NO KEY UPDATE
            ) s;
        END IF;

        EXIT WHEN batch_ids IS NULL OR cardinality(batch_ids) = 0;
        last_aid := batch_ids[cardinality(batch_ids)];

        WITH target_ids AS (
            SELECT unnest(batch_ids) AS aid
        ),
        like_counts AS (
            SELECT aid, COUNT(*)::integer AS cnt
            FROM likes
            WHERE aid = ANY(batch_ids)
            GROUP BY aid
        ),
        fav_counts AS (
            SELECT aid, COUNT(*)::integer AS cnt
            FROM favorites
            WHERE aid = ANY(batch_ids)
            GROUP BY aid
        ),
        comment_counts AS (
            SELECT target AS aid, COUNT(*)::integer AS cnt
            FROM comments
            WHERE target = ANY(batch_ids)
              AND status = 'active'
            GROUP BY target
        ),
        expected AS (
            SELECT
                t.aid,
                COALESCE(lc.cnt, 0) AS likes_cnt,
                COALESCE(fc.cnt, 0) AS favorite_cnt,
                COALESCE(cc.cnt, 0) AS comments_cnt
            FROM target_ids t
            LEFT JOIN like_counts lc ON lc.aid = t.aid
            LEFT JOIN fav_counts fc ON fc.aid = t.aid
            LEFT JOIN comment_counts cc ON cc.aid = t.aid
        )
        UPDATE articles a
        SET likes = e.likes_cnt,
            favorite = e.favorite_cnt,
            comments = e.comments_cnt
        FROM expected e
        WHERE a.aid = e.aid
          AND (
              a.likes IS DISTINCT FROM e.likes_cnt
              OR a.favorite IS DISTINCT FROM e.favorite_cnt
              OR a.comments IS DISTINCT FROM e.comments_cnt
          );

        COMMIT;
    END LOOP;

    -- Reconcile comments (likes) in keyset batches.
    last_cid := NULL;
    LOOP
        IF last_cid IS NULL THEN
            SELECT array_agg(cid ORDER BY cid)
            INTO batch_ids
            FROM (
                SELECT cid
                FROM comments
                ORDER BY cid
                LIMIT batch_size
                FOR NO KEY UPDATE
            ) s;
        ELSE
            SELECT array_agg(cid ORDER BY cid)
            INTO batch_ids
            FROM (
                SELECT cid
                FROM comments
                WHERE cid > last_cid
                ORDER BY cid
                LIMIT batch_size
                FOR NO KEY UPDATE
            ) s;
        END IF;

        EXIT WHEN batch_ids IS NULL OR cardinality(batch_ids) = 0;
        last_cid := batch_ids[cardinality(batch_ids)];

        WITH target_ids AS (
            SELECT unnest(batch_ids) AS cid
        ),
        like_counts AS (
            SELECT cid, COUNT(*)::integer AS cnt
            FROM commentlikes
            WHERE cid = ANY(batch_ids)
            GROUP BY cid
        ),
        expected AS (
            SELECT
                t.cid,
                COALESCE(lc.cnt, 0) AS likes_cnt
            FROM target_ids t
            LEFT JOIN like_counts lc ON lc.cid = t.cid
        )
        UPDATE comments c
        SET likes = e.likes_cnt
        FROM expected e
        WHERE c.cid = e.cid
          AND c.likes IS DISTINCT FROM e.likes_cnt;

        COMMIT;
    END LOOP;
END;
$$;

CALL reconcile_counts_batched(500);
