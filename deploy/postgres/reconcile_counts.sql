-- Reconcile denormalized counters with source tables.
-- Run by K8s CronJob or manually: psql -f reconcile_counts.sql
BEGIN ISOLATION LEVEL READ COMMITTED;

-- Use the same article row locks as the event consumer. The subsequent UPDATE
-- reads a fresh snapshot and touches only the rows acquired here.
CREATE TEMP TABLE reconcile_article_ids ON COMMIT DROP AS
SELECT aid FROM articles ORDER BY aid FOR NO KEY UPDATE;

UPDATE articles a
SET likes = COALESCE((SELECT COUNT(*) FROM likes l WHERE l.aid = a.aid), 0),
    favorite = COALESCE((SELECT COUNT(*) FROM favorites f WHERE f.aid = a.aid), 0),
    comments = COALESCE((SELECT COUNT(*) FROM comments c WHERE c.target = a.aid AND c.status = 'active'), 0)
FROM reconcile_article_ids locked
WHERE a.aid = locked.aid;

UPDATE comments c
SET likes = COALESCE((SELECT COUNT(*) FROM commentlikes cl WHERE cl.cid = c.cid), 0);

COMMIT;
