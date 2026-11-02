-- Reconcile denormalized counters with source tables.
-- Run by K8s CronJob or manually: psql -f reconcile_counts.sql
BEGIN;

UPDATE articles a
SET likes = COALESCE((SELECT COUNT(*) FROM likes l WHERE l.aid = a.aid), 0),
    favorite = COALESCE((SELECT COUNT(*) FROM favorites f WHERE f.aid = a.aid), 0),
    comments = COALESCE((SELECT COUNT(*) FROM comments c WHERE c.target = a.aid AND c.status = 'active'), 0);

UPDATE comments c
SET likes = COALESCE((SELECT COUNT(*) FROM commentlikes cl WHERE cl.cid = c.cid), 0);

COMMIT;
