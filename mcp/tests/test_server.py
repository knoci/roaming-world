import http.client
import json
import os
import socket
import threading
import unittest
from unittest.mock import patch

from mcp import server as mcp_server
from mcp.server import MCPHandler, make_http_server


class MCPHandlerSecurityAndRetrievalTests(unittest.TestCase):
    def test_invalid_upstream_schemes_rejected_on_init(self):
        env_keys = (
            "SCENE_API",
            "FOOD_API",
            "COMMENT_API",
            "ARTICLE_API",
            "AUDIOBOOK_API",
            "PLAN_API",
            "AI_API",
        )
        for env_key in env_keys:
            for bad_url in (
                "file:///etc/passwd",
                "ftp://example.com",
                "http://",
                "gopher://127.0.0.1:70",
            ):
                with self.subTest(env_key=env_key, bad_url=bad_url):
                    with patch.dict(os.environ, {env_key: bad_url}):
                        with self.assertRaises(ValueError):
                            MCPHandler(fetch_json=lambda _url: {})

    def test_path_traversal_and_invalid_ids_blocked(self):
        called_urls = []
        handler = MCPHandler(fetch_json=lambda url: called_urls.append(url) or {})

        bad_ids = [
            "../../metrics",
            "../scene/search",
            "a/b",
            "",
            "   ",
            "id?foo=bar",
            "id#frag",
            "a" * 65,
            "valid_id\n",
            12345,
            None,
        ]
        id_tools = (
            ("get_scene", "sid"),
            ("get_article", "aid"),
            ("list_article_comments", "aid"),
            ("get_plan", "plan_id"),
            ("get_audiobook_details", "bid"),
        )
        for tool, key in id_tools:
            for bad in bad_ids:
                with self.subTest(tool=tool, bad=bad):
                    res = handler.handle(
                        {
                            "jsonrpc": "2.0",
                            "id": 1,
                            "method": "tools/call",
                            "params": {"name": tool, "arguments": {key: bad}},
                        }
                    )
                    self.assertIn("error", res)
                    self.assertEqual(res["error"]["code"], -32602)
        self.assertEqual(called_urls, [])

    def test_valid_id_calls_upstream_with_quoted_id(self):
        called_urls = []
        handler = MCPHandler(
            fetch_json=lambda url: called_urls.append(url) or {"sid": "scene_01-A"}
        )
        res = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 2,
                "method": "tools/call",
                "params": {"name": "get_scene", "arguments": {"sid": "scene_01-A"}},
            }
        )
        self.assertIn("result", res)
        self.assertFalse(res["result"]["isError"])
        self.assertEqual(len(called_urls), 1)
        self.assertTrue(called_urls[0].endswith("/v1/scene/scene_01-A"))

    def test_invalid_query_and_limit_rejected_with_32602(self):
        called_urls = []
        handler = MCPHandler(fetch_json=lambda url: called_urls.append(url) or {})

        bad_args_list = [
            {"query": ""},
            {"query": "   "},
            {"query": "x" * 501},
            {"query": 123},
            {"query": "杭州", "limit": 0},
            {"query": "杭州", "limit": 21},
            {"query": "杭州", "limit": -3},
            {"query": "杭州", "limit": True},
            {"query": "杭州", "limit": "5"},
            {"query": "杭州", "page": 0},
            {"query": "杭州", "page": False},
        ]
        for bad_args in bad_args_list:
            with self.subTest(args=bad_args):
                res = handler.handle(
                    {
                        "jsonrpc": "2.0",
                        "id": 3,
                        "method": "tools/call",
                        "params": {"name": "search_scenes", "arguments": bad_args},
                    }
                )
                self.assertIn("error", res)
                self.assertEqual(res["error"]["code"], -32602)
        self.assertEqual(called_urls, [])

    def test_search_articles_matches_title_and_projects_truncated_output(self):
        long_content = "这是一段很长的游记正文" * 40

        def fake_fetch(url):
            self.assertIn("/v1/article?page=1&limit=50&way=time", url)
            return {
                "articles": [
                    {
                        "aid": "a1",
                        "title": "西湖秋日漫游指南",
                        "name": "作者甲",
                        "likes": 42,
                        "publishedAt": "2026-10-01T10:00:00Z",
                        "content": long_content,
                        "extra_secret_field": "should-not-appear",
                    },
                    {
                        "aid": "a2",
                        "title": "无关标题",
                        "name": "作者乙",
                        "content": "无关内容",
                    },
                ]
            }

        handler = MCPHandler(fetch_json=fake_fetch)
        res = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 4,
                "method": "tools/call",
                "params": {
                    "name": "search_articles",
                    "arguments": {"query": "西湖秋日", "limit": 5},
                },
            }
        )
        self.assertFalse(res["result"]["isError"])
        envelope = json.loads(res["result"]["content"][0]["text"])
        self.assertEqual(
            set(envelope.keys()),
            {"items", "page", "limit", "total", "has_more", "truncated"},
        )
        self.assertEqual(envelope["page"], 1)
        self.assertEqual(envelope["limit"], 5)
        self.assertEqual(envelope["total"], 1)
        self.assertFalse(envelope["has_more"])
        self.assertTrue(envelope["truncated"])
        items = envelope["items"]
        self.assertEqual(len(items), 1)
        item = items[0]
        self.assertEqual(
            set(item.keys()),
            {"aid", "title", "name", "likes", "publishedAt", "excerpt"},
        )
        self.assertEqual(item["aid"], "a1")
        self.assertEqual(item["title"], "西湖秋日漫游指南")
        self.assertEqual(item["likes"], 42)
        self.assertLessEqual(len(item["excerpt"]), 250)
        self.assertNotIn("extra_secret_field", item)

    def test_search_scenes_uses_backend_search_endpoint_and_projects(self):
        seen_urls = []
        long_desc = "西湖十景之一" * 60

        def fake_fetch(url):
            seen_urls.append(url)
            return {
                "scenes": [
                    {
                        "sid": "s1",
                        "name": "断桥残雪",
                        "location": "杭州",
                        "describe": long_desc,
                        "article": "huge raw html body" * 100,
                    }
                ],
                "total": 10,
            }

        handler = MCPHandler(fetch_json=fake_fetch)
        res = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 5,
                "method": "tools/call",
                "params": {
                    "name": "search_scenes",
                    "arguments": {"query": "西湖/断桥", "limit": 3},
                },
            }
        )
        self.assertEqual(len(seen_urls), 1)
        self.assertIn(
            "/v1/scene/search?keyword=%E8%A5%BF%E6%B9%96%2F%E6%96%AD%E6%A1%A5&page=1&pagesize=3",
            seen_urls[0],
        )
        envelope = json.loads(res["result"]["content"][0]["text"])
        self.assertTrue(envelope["has_more"])
        self.assertTrue(envelope["truncated"])
        self.assertEqual(envelope["total"], 10)
        items = envelope["items"]
        self.assertEqual(len(items), 1)
        self.assertEqual(set(items[0].keys()), {"sid", "name", "location", "describe"})
        self.assertLessEqual(len(items[0]["describe"]), 250)

    def test_search_scenes_falls_back_when_backend_search_empty(self):
        seen_urls = []

        def fake_fetch(url):
            seen_urls.append(url)
            if "/v1/scene/search?" in url:
                return {"scenes": []}
            return {
                "scenes": [
                    {
                        "sid": "s2",
                        "name": "灵隐寺",
                        "location": "杭州",
                        "describe": "千年古刹",
                    },
                ]
            }

        handler = MCPHandler(fetch_json=fake_fetch)
        res = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 6,
                "method": "tools/call",
                "params": {
                    "name": "search_scenes",
                    "arguments": {"query": "杭州", "limit": 5},
                },
            }
        )
        self.assertEqual(len(seen_urls), 2)
        self.assertIn("/v1/scene/search?", seen_urls[0])
        self.assertIn("/v1/scene?page=1&pagesize=50", seen_urls[1])
        envelope = json.loads(res["result"]["content"][0]["text"])
        items = envelope["items"]
        self.assertEqual(len(items), 1)
        self.assertEqual(items[0]["sid"], "s2")
        self.assertFalse(envelope["truncated"])

    def test_search_foods_paginates_matches_recipe_and_projects(self):
        seen_urls = []

        def fake_fetch(url):
            seen_urls.append(url)
            return {
                "foods": [
                    {
                        "fid": "f1",
                        "name": "龙井虾仁",
                        "location": "杭州",
                        "describe": "清香鲜嫩" * 80,
                        "recipe": "河虾仁配明前茶芽快炒",
                        "extra": "drop-me",
                    }
                ]
            }

        handler = MCPHandler(fetch_json=fake_fetch)
        res = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 7,
                "method": "tools/call",
                "params": {
                    "name": "search_foods",
                    "arguments": {"query": "明前茶芽", "limit": 5},
                },
            }
        )
        self.assertEqual(len(seen_urls), 1)
        self.assertTrue(seen_urls[0].endswith("/v1/food?page=1&limit=50"))
        envelope = json.loads(res["result"]["content"][0]["text"])
        self.assertTrue(envelope["truncated"])
        items = envelope["items"]
        self.assertEqual(len(items), 1)
        self.assertEqual(set(items[0].keys()), {"fid", "name", "location", "describe"})
        self.assertLessEqual(len(items[0]["describe"]), 250)

    def test_non_dict_json_returns_32600(self):
        handler = MCPHandler(fetch_json=lambda _url: {})
        for payload in ([], [1, 2], "hello", 42, None, True):
            with self.subTest(payload=payload):
                res = handler.handle(payload)
                self.assertEqual(res["error"]["code"], -32600)

    def test_upstream_failure_sanitized_without_leaking_url(self):
        def failing_fetch(url):
            raise OSError(f"Connection refused to {url}")

        with patch.dict(os.environ, {"SCENE_API": "http://10.99.88.77:18001"}):
            handler = MCPHandler(fetch_json=failing_fetch)
            res = handler.handle(
                {
                    "jsonrpc": "2.0",
                    "id": 8,
                    "method": "tools/call",
                    "params": {"name": "get_scene", "arguments": {"sid": "scene_1"}},
                }
            )
        self.assertIn("result", res)
        self.assertTrue(res["result"]["isError"])
        text = res["result"]["content"][0]["text"]
        self.assertNotIn("10.99.88.77", text)
        self.assertNotIn("18001", text)
        self.assertNotIn("http://", text)


class MCPPhase3ToolsResourcesPromptsTests(unittest.TestCase):
    def test_initialize_negotiates_protocol_version_and_capabilities(self):
        handler = MCPHandler(fetch_json=lambda _url: {})
        for requested, expected in (
            ("2025-03-26", "2025-03-26"),
            ("2024-11-05", "2024-11-05"),
            ("2099-01-01", "2025-03-26"),
            (None, "2025-03-26"),
        ):
            with self.subTest(requested=requested):
                params = {"protocolVersion": requested} if requested else {}
                res = handler.handle(
                    {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": params}
                )
                result = res["result"]
                self.assertEqual(result["protocolVersion"], expected)
                self.assertEqual(
                    set(result["capabilities"].keys()),
                    {"tools", "resources", "prompts"},
                )

    def test_tools_list_has_all_13_tools_and_annotations(self):
        handler = MCPHandler(fetch_json=lambda _url: {})
        res = handler.handle({"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
        tools = res["result"]["tools"]
        names = [t["name"] for t in tools]
        expected_names = {
            "search_scenes",
            "get_scene",
            "search_foods",
            "get_random_food",
            "search_articles",
            "get_article",
            "list_article_comments",
            "list_public_plans",
            "get_plan",
            "search_audiobooks",
            "get_audiobook_details",
            "search_knowledge_base",
            "save_travel_plan_draft",
        }
        self.assertEqual(set(names), expected_names)
        for t in tools:
            self.assertIn("inputSchema", t)
            self.assertIn("annotations", t)
            ann = t["annotations"]
            self.assertIn("readOnlyHint", ann)
            self.assertIn("idempotentHint", ann)
            self.assertIn("destructiveHint", ann)
            self.assertFalse(ann["destructiveHint"])
            self.assertTrue(ann["idempotentHint"])
            if t["name"] == "save_travel_plan_draft":
                self.assertFalse(ann["readOnlyHint"])
            else:
                self.assertTrue(ann["readOnlyHint"])

    def test_get_random_food(self):
        seen = []

        def fake_fetch(url):
            seen.append(url)
            return {
                "fid": "f9",
                "name": "东坡肉",
                "location": "杭州",
                "describe": "肥而不腻",
                "recipe": "五花肉慢炖",
            }

        handler = MCPHandler(fetch_json=fake_fetch)
        res = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 10,
                "method": "tools/call",
                "params": {"name": "get_random_food", "arguments": {}},
            }
        )
        self.assertFalse(res["result"]["isError"])
        self.assertTrue(seen[0].endswith("/v1/food/random"))
        data = json.loads(res["result"]["content"][0]["text"])
        self.assertEqual(data["name"], "东坡肉")
        self.assertEqual(data["recipe"], "五花肉慢炖")

    def test_search_articles_sort_by_validation(self):
        seen = []

        def fake_fetch(url):
            seen.append(url)
            return {"articles": []}

        handler = MCPHandler(fetch_json=fake_fetch)
        for way in ("time", "likes", "favorite"):
            res = handler.handle(
                {
                    "jsonrpc": "2.0",
                    "id": 11,
                    "method": "tools/call",
                    "params": {
                        "name": "search_articles",
                        "arguments": {"query": "西湖", "sort_by": way},
                    },
                }
            )
            self.assertFalse(res["result"]["isError"])
            self.assertIn(f"way={way}", seen[-1])

        bad = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 12,
                "method": "tools/call",
                "params": {
                    "name": "search_articles",
                    "arguments": {"query": "西湖", "sort_by": "invalid"},
                },
            }
        )
        self.assertEqual(bad["error"]["code"], -32602)

    def test_get_article_truncates_content_over_4000_chars(self):
        handler = MCPHandler(
            fetch_json=lambda _url: {
                "aid": "art_1",
                "title": "长文",
                "content": "A" * 5000,
            }
        )
        res = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 13,
                "method": "tools/call",
                "params": {"name": "get_article", "arguments": {"aid": "art_1"}},
            }
        )
        self.assertFalse(res["result"]["isError"])
        data = json.loads(res["result"]["content"][0]["text"])
        self.assertTrue(data["truncated"])
        self.assertLessEqual(len(data["content"]), 4000)

    def test_list_article_comments(self):
        seen = []

        def fake_fetch(url):
            seen.append(url)
            return {
                "comments": [
                    {
                        "cid": "c1",
                        "aid": "art_1",
                        "uid": "u1",
                        "content": "很棒的攻略" * 80,
                        "created_at": "2026-10-01",
                    }
                ],
                "total": 12,
            }

        handler = MCPHandler(fetch_json=fake_fetch)
        res = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 14,
                "method": "tools/call",
                "params": {
                    "name": "list_article_comments",
                    "arguments": {"aid": "art_1", "page": 1, "limit": 10},
                },
            }
        )
        self.assertFalse(res["result"]["isError"])
        self.assertIn("/v1/comment?aid=art_1&page=1&limit=10", seen[0])
        env = json.loads(res["result"]["content"][0]["text"])
        self.assertEqual(env["total"], 12)
        self.assertTrue(env["has_more"])
        self.assertTrue(env["truncated"])
        self.assertLessEqual(len(env["items"][0]["content"]), 250)

    def test_list_public_plans_and_get_plan(self):
        seen = []

        def fake_fetch(url):
            seen.append(url)
            if "/v1/plan/plan_1" in url:
                return {"id": "plan_1", "title": "成都三日游", "city": "成都"}
            return {
                "plans": [
                    {"id": "plan_1", "title": "成都三日游", "city": "成都", "budget": 2000},
                    {"id": "plan_2", "title": "西安古都行", "city": "西安", "budget": 3000},
                ]
            }

        handler = MCPHandler(fetch_json=fake_fetch)
        res = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 15,
                "method": "tools/call",
                "params": {
                    "name": "list_public_plans",
                    "arguments": {"query": "成都", "page": 1, "limit": 5},
                },
            }
        )
        self.assertIn("/v1/plan?scope=public&page=1&limit=50", seen[0])
        env = json.loads(res["result"]["content"][0]["text"])
        self.assertEqual(len(env["items"]), 1)
        self.assertEqual(env["items"][0]["id"], "plan_1")

        res2 = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 16,
                "method": "tools/call",
                "params": {"name": "get_plan", "arguments": {"plan_id": "plan_1"}},
            }
        )
        self.assertFalse(res2["result"]["isError"])
        self.assertEqual(json.loads(res2["result"]["content"][0]["text"])["city"], "成都")

    def test_search_audiobooks_and_get_audiobook_details(self):
        seen = []

        def fake_fetch(url):
            seen.append(url)
            if url.endswith("/v1/audiobook/book_1"):
                return {"bid": "book_1", "name": "江南风物志", "author": "李明"}
            return {
                "audiobooks": [
                    {
                        "bid": "book_1",
                        "name": "江南风物志",
                        "author": "李明",
                        "description": "讲述江南水乡故事",
                    },
                    {
                        "bid": "book_2",
                        "name": "西北行记",
                        "author": "张华",
                        "description": "丝绸之路见闻",
                    },
                ]
            }

        handler = MCPHandler(fetch_json=fake_fetch)
        res = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 17,
                "method": "tools/call",
                "params": {
                    "name": "search_audiobooks",
                    "arguments": {"query": "江南", "limit": 5},
                },
            }
        )
        self.assertTrue(seen[0].endswith("/v1/audiobook"))
        env = json.loads(res["result"]["content"][0]["text"])
        self.assertEqual(len(env["items"]), 1)
        self.assertEqual(env["items"][0]["bid"], "book_1")

        res2 = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 18,
                "method": "tools/call",
                "params": {
                    "name": "get_audiobook_details",
                    "arguments": {"bid": "book_1"},
                },
            }
        )
        self.assertFalse(res2["result"]["isError"])
        self.assertEqual(json.loads(res2["result"]["content"][0]["text"])["author"], "李明")

    def test_search_knowledge_base_posts_to_ai_chat_with_headers(self):
        calls = []

        def fake_request(method, url, headers=None, json_body=None):
            calls.append((method, url, headers, json_body))
            return {
                "answer": "西湖推荐春秋两季游览。",
                "sources": [
                    {"id": "s1", "title": "西湖", "type": "scene", "excerpt": "断桥残雪" * 80}
                ],
            }

        with patch.dict(os.environ, {"AI_GATEWAY_TOKEN": "ai-secret"}):
            handler = MCPHandler(request_json=fake_request)
            res = handler.handle(
                {
                    "jsonrpc": "2.0",
                    "id": 19,
                    "method": "tools/call",
                    "params": {
                        "name": "search_knowledge_base",
                        "arguments": {"query": "西湖什么时候去最好", "top_k": 3},
                    },
                },
                context={
                    "request_id": "req-123",
                    "traceparent": "00-abc-def-01",
                    "user_id": "u42",
                },
            )
        self.assertFalse(res["result"]["isError"])
        method, url, headers, body = calls[0]
        self.assertEqual(method, "POST")
        self.assertTrue(url.endswith("/v1/ai/chat"))
        self.assertEqual(headers["X-AI-Gateway-Token"], "ai-secret")
        self.assertEqual(headers["X-Request-ID"], "req-123")
        self.assertEqual(headers["traceparent"], "00-abc-def-01")
        self.assertEqual(headers["X-User-ID"], "u42")
        self.assertEqual(body["top_k"], 3)
        data = json.loads(res["result"]["content"][0]["text"])
        self.assertEqual(data["answer"], "西湖推荐春秋两季游览。")
        self.assertTrue(data["truncated"])
        self.assertLessEqual(len(data["sources"][0]["excerpt"]), 250)

    def test_save_travel_plan_draft_validation_and_forwarding(self):
        calls = []

        def fake_request(method, url, headers=None, json_body=None):
            calls.append((method, url, headers, json_body))
            return {"id": "plan_new", "status": "draft"}

        handler = MCPHandler(request_json=fake_request)
        valid_args = {
            "title": "杭州周末行",
            "city": "杭州",
            "start_date": "2026-11-01",
            "end_date": "2026-11-03",
            "budget": 1500,
            "days": [{"day": 1, "spots": ["西湖"]}],
            "idempotency_key": "idem-001",
        }

        # Missing user identity -> -32602
        res_no_user = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 20,
                "method": "tools/call",
                "params": {"name": "save_travel_plan_draft", "arguments": valid_args},
            }
        )
        self.assertEqual(res_no_user["error"]["code"], -32602)

        # Missing idempotency_key -> -32602
        no_idem = dict(valid_args, idempotency_key="")
        res_no_idem = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 21,
                "method": "tools/call",
                "params": {"name": "save_travel_plan_draft", "arguments": no_idem},
            },
            context={"user_id": "u1"},
        )
        self.assertEqual(res_no_idem["error"]["code"], -32602)

        # Invalid budget / dates -> -32602
        for bad_override in (
            {"budget": -10},
            {"budget": True},
            {"start_date": "2026/11/01"},
            {"end_date": "2026-10-30"},
        ):
            bad_args = dict(valid_args, **bad_override)
            res_bad = handler.handle(
                {
                    "jsonrpc": "2.0",
                    "id": 22,
                    "method": "tools/call",
                    "params": {"name": "save_travel_plan_draft", "arguments": bad_args},
                },
                context={"user_id": "u1"},
            )
            self.assertEqual(res_bad["error"]["code"], -32602)

        self.assertEqual(calls, [])

        # Valid call with X-User-ID + Authorization + Idempotency-Key
        res_ok = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 23,
                "method": "tools/call",
                "params": {"name": "save_travel_plan_draft", "arguments": valid_args},
            },
            context={
                "user_id": "u1",
                "authorization": "Bearer user-jwt",
                "request_id": "req-99",
            },
        )
        self.assertFalse(res_ok["result"]["isError"])
        self.assertEqual(len(calls), 1)
        method, url, headers, body = calls[0]
        self.assertEqual(method, "POST")
        self.assertTrue(url.endswith("/v1/plan"))
        self.assertEqual(headers["X-User-ID"], "u1")
        self.assertEqual(headers["Authorization"], "Bearer user-jwt")
        self.assertEqual(headers["Idempotency-Key"], "idem-001")
        self.assertEqual(headers["X-Request-ID"], "req-99")
        self.assertEqual(body["status"], "draft")

    def test_resources_list_and_read_and_traversal_rejection(self):
        seen = []

        def fake_fetch(url):
            seen.append(url)
            if "/v1/scene/s1" in url:
                return {"sid": "s1", "name": "西湖"}
            if "/v1/article/a1" in url:
                return {"aid": "a1", "content": "B" * 4500}
            if "/v1/plan/p1" in url:
                return {"id": "p1", "title": "川西行"}
            return {}

        handler = MCPHandler(fetch_json=fake_fetch)
        listed = handler.handle({"jsonrpc": "2.0", "id": 30, "method": "resources/list"})
        uris = [r["uri"] for r in listed["result"]["resources"]]
        self.assertEqual(
            uris,
            ["roaming://scene/{sid}", "roaming://article/{aid}", "roaming://plan/{id}"],
        )

        # Valid reads
        r_scene = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 31,
                "method": "resources/read",
                "params": {"uri": "roaming://scene/s1"},
            }
        )
        self.assertEqual(r_scene["result"]["contents"][0]["uri"], "roaming://scene/s1")
        self.assertEqual(json.loads(r_scene["result"]["contents"][0]["text"])["sid"], "s1")

        r_art = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 32,
                "method": "resources/read",
                "params": {"uri": "roaming://article/a1"},
            }
        )
        art_data = json.loads(r_art["result"]["contents"][0]["text"])
        self.assertTrue(art_data["truncated"])
        self.assertLessEqual(len(art_data["content"]), 4000)

        r_plan = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 33,
                "method": "resources/read",
                "params": {"uri": "roaming://plan/p1"},
            }
        )
        self.assertEqual(json.loads(r_plan["result"]["contents"][0]["text"])["id"], "p1")

        # Traversal & invalid URIs rejected with -32602
        before_count = len(seen)
        for bad_uri in (
            "roaming://scene/../../metrics",
            "roaming://article/a/b",
            "roaming://plan/p1?x=1",
            "roaming://unknown/id1",
            "file:///etc/passwd",
            "",
        ):
            with self.subTest(uri=bad_uri):
                res = handler.handle(
                    {
                        "jsonrpc": "2.0",
                        "id": 34,
                        "method": "resources/read",
                        "params": {"uri": bad_uri},
                    }
                )
                self.assertEqual(res["error"]["code"], -32602)
        self.assertEqual(len(seen), before_count)

    def test_prompts_list_and_get(self):
        handler = MCPHandler(fetch_json=lambda _url: {})
        listed = handler.handle({"jsonrpc": "2.0", "id": 40, "method": "prompts/list"})
        prompt_names = {p["name"] for p in listed["result"]["prompts"]}
        self.assertEqual(
            prompt_names, {"travel_itinerary_planner", "local_food_curator"}
        )

        p1 = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 41,
                "method": "prompts/get",
                "params": {
                    "name": "travel_itinerary_planner",
                    "arguments": {"destination": "苏州", "days": "4", "style": "cultural"},
                },
            }
        )
        text1 = p1["result"]["messages"][0]["content"]["text"]
        self.assertIn("苏州", text1)
        self.assertIn("4", text1)
        self.assertIn("cultural", text1)

        p2 = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 42,
                "method": "prompts/get",
                "params": {
                    "name": "local_food_curator",
                    "arguments": {"city": "广州", "preferences": "早茶"},
                },
            }
        )
        text2 = p2["result"]["messages"][0]["content"]["text"]
        self.assertIn("广州", text2)
        self.assertIn("早茶", text2)

        # Missing required arg / unknown prompt -> -32602
        bad1 = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 43,
                "method": "prompts/get",
                "params": {"name": "travel_itinerary_planner", "arguments": {}},
            }
        )
        self.assertEqual(bad1["error"]["code"], -32602)

        bad2 = handler.handle(
            {
                "jsonrpc": "2.0",
                "id": 44,
                "method": "prompts/get",
                "params": {"name": "no_such_prompt", "arguments": {}},
            }
        )
        self.assertEqual(bad2["error"]["code"], -32602)


class MCPHTTPServerSecurityTests(unittest.TestCase):
    def setUp(self):
        self.env_patch = patch.dict(
            os.environ, {"MCP_TOKEN": "  secret-token-123  "}, clear=False
        )
        self.env_patch.start()
        os.environ.pop("MCP_ALLOWED_ORIGINS", None)
        self.handler = MCPHandler(fetch_json=lambda _url: {"scenes": []})
        self.server = make_http_server("127.0.0.1", 0, handler=self.handler)
        self.port = self.server.server_address[1]
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        self.handler.close()
        self.env_patch.stop()

    def _raw_post(
        self, raw_headers: dict[str, str], body: bytes
    ) -> tuple[int, dict, dict[str, str]]:
        conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=3)
        try:
            conn.request("POST", "/mcp", body=body, headers=raw_headers)
            resp = conn.getresponse()
            raw = resp.read().decode("utf-8")
            data = json.loads(raw) if raw else {}
            resp_headers = {k.lower(): v for k, v in resp.getheaders()}
            return resp.status, data, resp_headers
        finally:
            conn.close()

    def _raw_get(self, path: str) -> tuple[int, str, dict[str, str]]:
        conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=3)
        try:
            conn.request("GET", path)
            resp = conn.getresponse()
            raw = resp.read().decode("utf-8")
            resp_headers = {k.lower(): v for k, v in resp.getheaders()}
            return resp.status, raw, resp_headers
        finally:
            conn.close()

    def test_accepts_x_mcp_token_and_bearer_token(self):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "ping"}).encode("utf-8")
        status1, data1, headers1 = self._raw_post(
            {
                "Content-Type": "application/json",
                "Content-Length": str(len(body)),
                "X-MCP-Token": "secret-token-123",
            },
            body,
        )
        self.assertEqual(status1, 200)
        self.assertEqual(data1["result"], {})
        self.assertTrue(headers1.get("mcp-session-id"))

        status2, data2, _ = self._raw_post(
            {
                "Content-Type": "application/json",
                "Content-Length": str(len(body)),
                "Authorization": "Bearer secret-token-123",
            },
            body,
        )
        self.assertEqual(status2, 200)
        self.assertEqual(data2["result"], {})

    def test_rejects_missing_or_wrong_token(self):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "ping"}).encode("utf-8")
        for headers in (
            {"Content-Type": "application/json", "Content-Length": str(len(body))},
            {
                "Content-Type": "application/json",
                "Content-Length": str(len(body)),
                "X-MCP-Token": "wrong",
            },
            {
                "Content-Type": "application/json",
                "Content-Length": str(len(body)),
                "Authorization": "Bearer wrong",
            },
        ):
            status, _, _ = self._raw_post(headers, body)
            self.assertEqual(status, 401)

    def test_rejects_cross_origin_and_allows_localhost_origin(self):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "ping"}).encode("utf-8")
        for bad_origin in ("https://evil.example", "http://localhost.evil.com", "null"):
            with self.subTest(origin=bad_origin):
                status, _, _ = self._raw_post(
                    {
                        "Content-Type": "application/json",
                        "Content-Length": str(len(body)),
                        "X-MCP-Token": "secret-token-123",
                        "Origin": bad_origin,
                    },
                    body,
                )
                self.assertEqual(status, 403)

        status, data, _ = self._raw_post(
            {
                "Content-Type": "application/json",
                "Content-Length": str(len(body)),
                "X-MCP-Token": "secret-token-123",
                "Origin": "http://localhost:3000",
            },
            body,
        )
        self.assertEqual(status, 200)
        self.assertEqual(data["result"], {})

    def test_rejects_oversized_content_length_with_413(self):
        req = (
            "POST /mcp HTTP/1.1\r\n"
            "Host: 127.0.0.1\r\n"
            "X-MCP-Token: secret-token-123\r\n"
            "Content-Type: application/json\r\n"
            f"Content-Length: {mcp_server.MAX_BODY_BYTES + 1}\r\n"
            "Connection: close\r\n\r\n"
        ).encode("ascii")
        with socket.create_connection(("127.0.0.1", self.port), timeout=3) as sock:
            sock.sendall(req)
            raw_resp = sock.recv(4096).decode("utf-8", errors="replace")
        self.assertTrue(
            raw_resp.startswith("HTTP/1.0 413") or raw_resp.startswith("HTTP/1.1 413"),
            raw_resp,
        )

    def test_rejects_negative_zero_missing_nonint_content_length_with_400(self):
        for cl_header in (
            "Content-Length: -5\r\n",
            "Content-Length: 0\r\n",
            "Content-Length: abc\r\n",
            "",
        ):
            with self.subTest(cl_header=cl_header):
                req = (
                    "POST /mcp HTTP/1.1\r\n"
                    "Host: 127.0.0.1\r\n"
                    "X-MCP-Token: secret-token-123\r\n"
                    "Content-Type: application/json\r\n"
                    f"{cl_header}"
                    "Connection: close\r\n\r\n"
                ).encode("ascii")
                with socket.create_connection(("127.0.0.1", self.port), timeout=3) as sock:
                    sock.sendall(req)
                    raw_resp = sock.recv(4096).decode("utf-8", errors="replace")
                self.assertTrue(
                    raw_resp.startswith("HTTP/1.0 400")
                    or raw_resp.startswith("HTTP/1.1 400"),
                    raw_resp,
                )

    def test_http_non_dict_json_returns_32600(self):
        body = b"[1, 2, 3]"
        status, data, _ = self._raw_post(
            {
                "Content-Type": "application/json",
                "Content-Length": str(len(body)),
                "X-MCP-Token": "secret-token-123",
            },
            body,
        )
        self.assertEqual(status, 200)
        self.assertEqual(data["error"]["code"], -32600)

    def test_main_http_mode_fails_fast_without_mcp_token(self):
        with patch.dict(os.environ, {"MCP_TOKEN": "   "}), patch(
            "sys.argv", ["server.py", "--port", "0"]
        ):
            with self.assertRaises(SystemExit) as ctx:
                mcp_server.main()
            self.assertNotEqual(ctx.exception.code, 0)

    def test_session_id_echo_and_delete_session(self):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "initialize"}).encode(
            "utf-8"
        )
        status, _, headers = self._raw_post(
            {
                "Content-Type": "application/json",
                "Content-Length": str(len(body)),
                "X-MCP-Token": "secret-token-123",
                "Mcp-Session-Id": "sess-abc-123",
            },
            body,
        )
        self.assertEqual(status, 200)
        self.assertEqual(headers.get("mcp-session-id"), "sess-abc-123")
        self.assertIn("sess-abc-123", self.server.mcp_sessions)

        conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=3)
        try:
            conn.request(
                "DELETE",
                "/mcp",
                headers={
                    "X-MCP-Token": "secret-token-123",
                    "Mcp-Session-Id": "sess-abc-123",
                },
            )
            resp = conn.getresponse()
            resp.read()
            self.assertEqual(resp.status, 204)
        finally:
            conn.close()
        self.assertNotIn("sess-abc-123", self.server.mcp_sessions)

    def test_healthz_readyz_and_metrics_endpoints(self):
        st_h, raw_h, _ = self._raw_get("/healthz")
        self.assertEqual(st_h, 200)
        self.assertEqual(json.loads(raw_h)["status"], "ok")

        st_r, raw_r, _ = self._raw_get("/readyz")
        self.assertEqual(st_r, 200)
        self.assertEqual(json.loads(raw_r)["status"], "ready")

        with patch.dict(os.environ, {"MCP_TOKEN": ""}):
            st_r2, _, _ = self._raw_get("/readyz")
            self.assertEqual(st_r2, 503)

        # Trigger a tool call so metrics are populated
        call_body = json.dumps(
            {
                "jsonrpc": "2.0",
                "id": 2,
                "method": "tools/call",
                "params": {"name": "search_scenes", "arguments": {"query": "杭州"}},
            }
        ).encode("utf-8")
        st_call, _, _ = self._raw_post(
            {
                "Content-Type": "application/json",
                "Content-Length": str(len(call_body)),
                "X-MCP-Token": "secret-token-123",
            },
            call_body,
        )
        self.assertEqual(st_call, 200)

        st_m, raw_m, headers_m = self._raw_get("/metrics")
        self.assertEqual(st_m, 200)
        self.assertIn("text/plain", headers_m.get("content-type", ""))
        self.assertIn("roaming_mcp_tool_calls_total", raw_m)
        self.assertIn('tool="search_scenes"', raw_m)
        self.assertIn("roaming_mcp_tool_duration_seconds", raw_m)

    def test_http_bearer_mcp_token_not_treated_as_user_identity_for_write_tool(self):
        body = json.dumps(
            {
                "jsonrpc": "2.0",
                "id": 9,
                "method": "tools/call",
                "params": {
                    "name": "save_travel_plan_draft",
                    "arguments": {
                        "title": "北京游",
                        "city": "北京",
                        "start_date": "2026-12-01",
                        "end_date": "2026-12-02",
                        "budget": 1000,
                        "idempotency_key": "idem-http-1",
                    },
                },
            }
        ).encode("utf-8")
        status, data, _ = self._raw_post(
            {
                "Content-Type": "application/json",
                "Content-Length": str(len(body)),
                "Authorization": "Bearer secret-token-123",
            },
            body,
        )
        self.assertEqual(status, 200)
        self.assertEqual(data["error"]["code"], -32602)


if __name__ == "__main__":
    unittest.main()
