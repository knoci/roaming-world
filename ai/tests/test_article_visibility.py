import os
import unittest
from unittest.mock import patch
from uuid import uuid4

import psycopg

from ai.app.db import ingest_all, search


@unittest.skipUnless(os.getenv("ROAMING_TEST_AI_POSTGRES_DSN"), "requires a migrated pgvector test database")
class ArticleVisibilityTest(unittest.TestCase):
    def test_live_article_visibility_and_index_cleanup(self):
        dsn = os.environ["ROAMING_TEST_AI_POSTGRES_DSN"]
        offline, deleted, blank, changed, still_public = [str(uuid4()) for _ in range(5)]
        article_ids = [offline, deleted, blank, changed, still_public]
        self.addCleanup(self.cleanup, dsn, article_ids)
        with psycopg.connect(dsn) as conn:
            with conn.cursor() as cur:
                cur.executemany(
                    """
                    INSERT INTO articles (aid, name, title, view, content, status)
                    VALUES (%s, 'Fixture author', 'Fixture title', '[]', 'Fixture old content', 'published')
                    """,
                    [(aid,) for aid in article_ids],
                )

        with patch.dict(os.environ, {"EMBEDDING_PROVIDER": "hash"}):
            with psycopg.connect(dsn) as conn:
                ingest_all(conn)
                self.assertEqual(set(article_ids), self.visible_ids(conn, article_ids))
                with conn.cursor() as cur:
                    cur.execute(
                        "SELECT id FROM ai_documents WHERE source_type = 'article' AND source_id = ANY(%s)",
                        ([offline, deleted, blank],),
                    )
                    stale_docs = [row[0] for row in cur.fetchall()]
                    self.assertEqual(len(stale_docs), 3)

            with psycopg.connect(dsn) as conn:
                with conn.cursor() as cur:
                    cur.execute("UPDATE articles SET status = 'offline' WHERE aid = %s", (offline,))
                    cur.execute("DELETE FROM articles WHERE aid = %s", (deleted,))
                    cur.execute("UPDATE articles SET content = '' WHERE aid = %s", (blank,))
                    cur.execute("UPDATE articles SET content = 'Fixture revised content' WHERE aid = %s", (changed,))

            with psycopg.connect(dsn) as conn:
                self.assertEqual({still_public}, self.visible_ids(conn, article_ids))
                ingest_all(conn)
                self.assertEqual({still_public, changed}, self.visible_ids(conn, article_ids))
                with conn.cursor() as cur:
                    cur.execute("SELECT count(*) FROM ai_documents WHERE id = ANY(%s)", (stale_docs,))
                    self.assertEqual(cur.fetchone()[0], 0)
                    cur.execute("SELECT count(*) FROM ai_chunks WHERE doc_id = ANY(%s)", (stale_docs,))
                    self.assertEqual(cur.fetchone()[0], 0)

    @staticmethod
    def visible_ids(conn, article_ids):
        return {row["source_id"] for row in search(conn, "Fixture content", 50) if row["source_id"] in article_ids}

    @staticmethod
    def cleanup(dsn, article_ids):
        with psycopg.connect(dsn) as conn:
            with conn.cursor() as cur:
                cur.execute("DELETE FROM articles WHERE aid = ANY(%s)", (article_ids,))
                cur.execute("DELETE FROM ai_documents WHERE source_type = 'article' AND source_id = ANY(%s)", (article_ids,))
