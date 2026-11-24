import asyncio
import json
import os
import unittest
from unittest.mock import patch

from ai.app import chunking, db, embeddings
from ai.app.grounding import extract_citations, sanitize_answer, verify_grounding
from ai.app.indexer import IncrementalIndexer, IndexEvent, parse_kafka_message
from ai.app.rag import HybridRetriever, rerank_candidates, rrf_fuse


class ChunkingTests(unittest.TestCase):
    def test_sentence_aware_cross_paragraph_overlap(self):
        text = (
            "西湖位于杭州市中心，四季风景各异。春季苏堤春晓最受游客欢迎。\n\n"
            "断桥残雪是冬季经典景观。灵隐寺距离西湖不远，香火鼎盛，值得安排半天游览。"
        )
        chunks = chunking.chunk_text(text, max_chars=45, overlap=14)
        self.assertGreaterEqual(len(chunks), 2)
        for ch in chunks:
            self.assertLessEqual(len(ch), 45)
        # Cross-paragraph sliding-window overlap: tail of chunk 0 appears at start of chunk 1
        tail_fragment = chunks[0][-8:]
        self.assertIn(tail_fragment, chunks[1])

    def test_contextualize_chunks_and_content_hash(self):
        raw = ["苏堤春晓景色迷人。", "可以乘船游湖。"]
        with_loc = chunking.contextualize_chunks(raw, source_type="scene", title="西湖", location="杭州")
        self.assertEqual(with_loc[0], "[scene|杭州] 西湖: 苏堤春晓景色迷人。")
        self.assertEqual(with_loc[1], "[scene|杭州] 西湖: 可以乘船游湖。")

        without_loc = chunking.contextualize_chunks(raw, source_type="article", title="杭州攻略")
        self.assertEqual(without_loc[0], "[article] 杭州攻略: 苏堤春晓景色迷人。")

        h1 = chunking.content_hash("scene", "西湖", "内容A", "杭州")
        h2 = chunking.content_hash("scene", "西湖", "内容A", "杭州")
        h3 = chunking.content_hash("scene", "西湖", "内容B", "杭州")
        self.assertEqual(h1, h2)
        self.assertEqual(len(h1), 64)
        self.assertNotEqual(h1, h3)


class EmbeddingsBatchTests(unittest.TestCase):
    def test_batch_hash_sync_and_async(self):
        with patch.dict(os.environ, {"AI_EMBEDDING_PROVIDER": "hash"}, clear=False):
            self.assertEqual(embeddings.current_embedding_model(), "hash")
            sync_vecs = embeddings.embed_batch(["西湖", "东坡肉"], batch_size=1)
            async_vecs = asyncio.run(embeddings.aembed_batch(["西湖", "东坡肉"], batch_size=1))
            self.assertEqual(len(sync_vecs), 2)
            self.assertEqual(sync_vecs, async_vecs)
            self.assertEqual(len(sync_vecs[0]), embeddings.DIM)

    def test_batch_openai_sync_preserves_index_order(self):
        v0 = [0.25] * embeddings.DIM
        v1 = [0.75] * embeddings.DIM

        class FakeResp:
            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

            def read(self):
                # Deliberately return out-of-order indices to verify sorting
                return json.dumps(
                    {"data": [{"index": 1, "embedding": v1}, {"index": 0, "embedding": v0}]}
                ).encode("utf-8")

        env = {
            "AI_EMBEDDING_PROVIDER": "openai",
            "AI_EMBEDDING_API_KEY": "k-test",
            "AI_EMBEDDING_MODEL": "BAAI/bge-m3",
        }
        with patch.dict(os.environ, env, clear=False), patch("urllib.request.urlopen", return_value=FakeResp()):
            self.assertEqual(embeddings.current_embedding_model(), "BAAI/bge-m3")
            vecs = embeddings.embed_batch(["a", "b"], batch_size=8)
        self.assertEqual(vecs, [v0, v1])


class HybridRagTests(unittest.TestCase):
    def test_rrf_fuse_boosts_items_when_dense_and_lexical_agree(self):
        dense = [
            {"source_type": "scene", "source_id": "s_dense_only", "chunk_no": 0, "title": "A", "content": "x", "score": 0.92},
            {"source_type": "scene", "source_id": "s_both", "chunk_no": 0, "title": "西湖", "content": "杭州西湖", "score": 0.85},
        ]
        lexical = [
            {"source_type": "scene", "source_id": "s_lex_only", "chunk_no": 0, "title": "B", "content": "y", "score": 0.95},
            {"source_type": "scene", "source_id": "s_both", "chunk_no": 0, "title": "西湖", "content": "杭州西湖", "score": 0.80},
        ]
        fused = rrf_fuse(dense, lexical, k=60)
        self.assertEqual(fused[0]["source_id"], "s_both")
        self.assertAlmostEqual(fused[0]["rrf_score"], 2.0 / 62.0, places=6)

    def test_rerank_doc_diversity_dedup_and_low_score_abstention(self):
        candidates = [
            {"source_type": "article", "source_id": "a1", "chunk_no": 0, "title": "杭州西湖攻略", "content": "杭州西湖必去景点推荐", "rrf_score": 0.03},
            {"source_type": "article", "source_id": "a1", "chunk_no": 1, "title": "杭州西湖攻略", "content": "杭州西湖游船攻略", "rrf_score": 0.029},
            {"source_type": "article", "source_id": "a1", "chunk_no": 2, "title": "杭州西湖攻略", "content": "杭州西湖苏堤漫步", "rrf_score": 0.028},
            {"source_type": "scene", "source_id": "s1", "chunk_no": 0, "title": "灵隐寺", "content": "杭州灵隐寺景区介绍", "rrf_score": 0.025},
        ]
        deduped = rerank_candidates("杭州西湖", candidates, top_k=5, min_score=0.01, max_per_doc=2)
        a1_chunks = [c for c in deduped if c["source_id"] == "a1"]
        self.assertEqual(len(a1_chunks), 2)
        self.assertTrue(any(c["source_id"] == "s1" for c in deduped))

        # Low-score irrelevant noise should be filtered out completely (enabling honest abstention)
        irrelevant = [
            {"source_type": "food", "source_id": "f9", "chunk_no": 0, "title": "冰岛鳕鱼", "content": "北大西洋海鲜", "score": 0.002},
        ]
        abstained = rerank_candidates("火星旅游攻略", irrelevant, top_k=5, min_score=0.01)
        self.assertEqual(abstained, [])

    def test_hybrid_retriever_with_injected_functions(self):
        def fake_vector(_q, _limit):
            return [
                {"source_type": "scene", "source_id": "s1", "chunk_no": 0, "title": "西湖", "content": "杭州西湖景点", "score": 0.88}
            ]

        def fake_lexical(_q, _limit):
            return [
                {"source_type": "scene", "source_id": "s1", "chunk_no": 0, "title": "西湖", "content": "杭州西湖景点", "score": 0.72}
            ]

        retriever = HybridRetriever(vector_fn=fake_vector, lexical_fn=fake_lexical)
        hits = retriever.retrieve("杭州西湖", top_k=3)
        self.assertEqual(len(hits), 1)
        self.assertEqual(hits[0]["source_id"], "s1")


class GroundingTests(unittest.TestCase):
    def test_verify_grounding_catches_hallucinated_and_computes_precision(self):
        allowed = [{"source_type": "scene", "source_id": "s1"}]
        answer = "推荐游览西湖 [scene:s1]，也可以参考虚构文章 [article:fake-999]。"

        self.assertEqual(extract_citations(answer), [("scene", "s1"), ("article", "fake-999")])
        report = verify_grounding(answer, allowed)
        self.assertEqual(report.valid_citations, [("scene", "s1")])
        self.assertEqual(report.hallucinated_citations, [("article", "fake-999")])
        self.assertAlmostEqual(report.citation_precision, 0.5)
        self.assertFalse(report.grounding_ok)

        cleaned = sanitize_answer(answer, allowed)
        self.assertIn("[scene:s1]", cleaned)
        self.assertNotIn("[article:fake-999]", cleaned)

    def test_verify_grounding_passes_grounded_and_explicit_abstention(self):
        allowed = [{"source_type": "scene", "source_id": "s1"}]
        grounded = verify_grounding("西湖风景秀丽 [scene:s1]。", allowed)
        self.assertTrue(grounded.grounding_ok)
        self.assertAlmostEqual(grounded.citation_precision, 1.0)

        abstain = verify_grounding("知识库中暂无关于该目的地的相关信息，无法确认。", [])
        self.assertTrue(abstain.is_abstention)
        self.assertTrue(abstain.grounding_ok)
        self.assertAlmostEqual(abstain.citation_precision, 1.0)


class IncrementalIndexerAndDBTests(unittest.TestCase):
    def test_indexer_skips_unchanged_hash_and_deletes_on_unpublish(self):
        docs_store: dict[tuple[str, str], dict] = {}
        chunks_store: dict[int, list[str]] = {}
        next_id = [0]

        class InMemoryCursor:
            def __init__(self):
                self._row = None

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

            def execute(self, sql, params=None):
                self._row = None
                if "SELECT id, content_hash, embedding_model" in sql:
                    key = (params[0], params[1])
                    doc = docs_store.get(key)
                    if doc:
                        self._row = (doc["id"], doc["content_hash"], doc["embedding_model"], doc["revision"])
                elif "INSERT INTO ai_documents" in sql:
                    key = (params[0], params[1])
                    if key not in docs_store:
                        next_id[0] += 1
                        doc_id = next_id[0]
                    else:
                        doc_id = docs_store[key]["id"]
                    docs_store[key] = {
                        "id": doc_id,
                        "content_hash": params[5],
                        "revision": params[6],
                        "embedding_model": params[7],
                    }
                    self._row = (doc_id,)
                elif "DELETE FROM ai_chunks" in sql:
                    chunks_store.pop(params[0], None)
                elif "INSERT INTO ai_chunks" in sql:
                    chunks_store.setdefault(params[0], []).append(params[2])
                elif "DELETE FROM ai_documents" in sql:
                    key = (params[0], params[1])
                    doc = docs_store.pop(key, None)
                    if doc:
                        chunks_store.pop(doc["id"], None)

            def fetchone(self):
                return self._row

        class InMemoryConn:
            def cursor(self):
                return InMemoryCursor()

        conn = InMemoryConn()
        indexer = IncrementalIndexer()
        ev = IndexEvent(
            event_type="upsert",
            source_type="scene",
            source_id="s1",
            title="西湖",
            content="西湖位于杭州。",
            location="杭州",
            url="/v1/scene/s1",
            revision=1,
        )

        with patch.dict(os.environ, {"AI_EMBEDDING_PROVIDER": "hash"}, clear=False):
            r1 = indexer.apply_event(conn, ev)
            self.assertEqual(r1["action"], "upserted")
            self.assertEqual(r1["chunks"], 1)

            # Same content_hash + model -> skipped without re-embedding
            with patch("ai.app.indexer.embed_batch", side_effect=AssertionError("should not re-embed")):
                r2 = indexer.apply_event(conn, ev)
            self.assertEqual(r2["action"], "skipped")

            # Unpublish via Kafka message -> deleted
            raw_msg = json.dumps(
                {"event_type": "unpublish", "source_type": "scene", "source_id": "s1"}
            ).encode("utf-8")
            del_ev = parse_kafka_message(raw_msg)
            self.assertIsNotNone(del_ev)
            self.assertEqual(del_ev.event_type, "delete")

            r3 = indexer.apply_event(conn, del_ev)
            self.assertEqual(r3["action"], "deleted")
            self.assertNotIn(("scene", "s1"), docs_store)

    def test_db_search_does_not_call_ensure_schema(self):
        class FakeCursor:
            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

            def execute(self, sql, params=None):
                pass

            def fetchall(self):
                return [("内容", "scene", "s1", "西湖", "/v1/scene/s1", 0.91)]

        class FakeConn:
            def cursor(self):
                return FakeCursor()

        with patch.object(db, "ensure_schema", side_effect=AssertionError("search must not call ensure_schema")):
            with patch.dict(os.environ, {"AI_EMBEDDING_PROVIDER": "hash"}, clear=False):
                rows = db.search(FakeConn(), "西湖", top_k=5)
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["source_id"], "s1")


if __name__ == "__main__":
    unittest.main()
