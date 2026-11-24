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
        for env_key in ("SCENE_API", "FOOD_API", "ARTICLE_API"):
            for bad_url in ("file:///etc/passwd", "ftp://example.com", "http://", "gopher://127.0.0.1:70"):
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
        for tool, key in (("get_scene", "sid"), ("get_article", "aid")):
            for bad in bad_ids:
                with self.subTest(tool=tool, bad=bad):
                    res = handler.handle({
                        "jsonrpc": "2.0",
                        "id": 1,
                        "method": "tools/call",
                        "params": {"name": tool, "arguments": {key: bad}},
                    })
                    self.assertIn("error", res)
                    self.assertEqual(res["error"]["code"], -32602)
        self.assertEqual(called_urls, [])

    def test_valid_id_calls_upstream_with_quoted_id(self):
        called_urls = []
        handler = MCPHandler(fetch_json=lambda url: called_urls.append(url) or {"sid": "scene_01-A"})
        res = handler.handle({
            "jsonrpc": "2.0",
            "id": 2,
            "method": "tools/call",
            "params": {"name": "get_scene", "arguments": {"sid": "scene_01-A"}},
        })
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
        ]
        for bad_args in bad_args_list:
            with self.subTest(args=bad_args):
                res = handler.handle({
                    "jsonrpc": "2.0",
                    "id": 3,
                    "method": "tools/call",
                    "params": {"name": "search_scenes", "arguments": bad_args},
                })
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
        res = handler.handle({
            "jsonrpc": "2.0",
            "id": 4,
            "method": "tools/call",
            "params": {"name": "search_articles", "arguments": {"query": "西湖秋日", "limit": 5}},
        })
        self.assertFalse(res["result"]["isError"])
        items = json.loads(res["result"]["content"][0]["text"])
        self.assertEqual(len(items), 1)
        item = items[0]
        self.assertEqual(set(item.keys()), {"aid", "title", "name", "likes", "publishedAt", "excerpt"})
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
                ]
            }

        handler = MCPHandler(fetch_json=fake_fetch)
        res = handler.handle({
            "jsonrpc": "2.0",
            "id": 5,
            "method": "tools/call",
            "params": {"name": "search_scenes", "arguments": {"query": "西湖/断桥", "limit": 3}},
        })
        self.assertEqual(len(seen_urls), 1)
        self.assertIn("/v1/scene/search?keyword=%E8%A5%BF%E6%B9%96%2F%E6%96%AD%E6%A1%A5&page=1&pagesize=3", seen_urls[0])
        items = json.loads(res["result"]["content"][0]["text"])
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
                    {"sid": "s2", "name": "灵隐寺", "location": "杭州", "describe": "千年古刹"},
                ]
            }

        handler = MCPHandler(fetch_json=fake_fetch)
        res = handler.handle({
            "jsonrpc": "2.0",
            "id": 6,
            "method": "tools/call",
            "params": {"name": "search_scenes", "arguments": {"query": "杭州", "limit": 5}},
        })
        self.assertEqual(len(seen_urls), 2)
        self.assertIn("/v1/scene/search?", seen_urls[0])
        self.assertIn("/v1/scene?page=1&pagesize=50", seen_urls[1])
        items = json.loads(res["result"]["content"][0]["text"])
        self.assertEqual(len(items), 1)
        self.assertEqual(items[0]["sid"], "s2")

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
        res = handler.handle({
            "jsonrpc": "2.0",
            "id": 7,
            "method": "tools/call",
            "params": {"name": "search_foods", "arguments": {"query": "明前茶芽", "limit": 5}},
        })
        self.assertEqual(len(seen_urls), 1)
        self.assertTrue(seen_urls[0].endswith("/v1/food?page=1&limit=50"))
        items = json.loads(res["result"]["content"][0]["text"])
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
            res = handler.handle({
                "jsonrpc": "2.0",
                "id": 8,
                "method": "tools/call",
                "params": {"name": "get_scene", "arguments": {"sid": "scene_1"}},
            })
        self.assertIn("result", res)
        self.assertTrue(res["result"]["isError"])
        text = res["result"]["content"][0]["text"]
        self.assertNotIn("10.99.88.77", text)
        self.assertNotIn("18001", text)
        self.assertNotIn("http://", text)


class MCPHTTPServerSecurityTests(unittest.TestCase):
    def setUp(self):
        self.env_patch = patch.dict(os.environ, {"MCP_TOKEN": "  secret-token-123  "}, clear=False)
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
        self.env_patch.stop()

    def _raw_post(self, raw_headers: dict[str, str], body: bytes) -> tuple[int, dict]:
        conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=3)
        try:
            conn.request("POST", "/mcp", body=body, headers=raw_headers)
            resp = conn.getresponse()
            data = json.loads(resp.read().decode("utf-8"))
            return resp.status, data
        finally:
            conn.close()

    def test_accepts_x_mcp_token_and_bearer_token(self):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "ping"}).encode("utf-8")
        status1, data1 = self._raw_post(
            {"Content-Type": "application/json", "Content-Length": str(len(body)), "X-MCP-Token": "secret-token-123"},
            body,
        )
        self.assertEqual(status1, 200)
        self.assertEqual(data1["result"], {})

        status2, data2 = self._raw_post(
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
            {"Content-Type": "application/json", "Content-Length": str(len(body)), "X-MCP-Token": "wrong"},
            {"Content-Type": "application/json", "Content-Length": str(len(body)), "Authorization": "Bearer wrong"},
        ):
            status, _ = self._raw_post(headers, body)
            self.assertEqual(status, 401)

    def test_rejects_cross_origin_and_allows_localhost_origin(self):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": "ping"}).encode("utf-8")
        for bad_origin in ("https://evil.example", "http://localhost.evil.com", "null"):
            with self.subTest(origin=bad_origin):
                status, _ = self._raw_post(
                    {
                        "Content-Type": "application/json",
                        "Content-Length": str(len(body)),
                        "X-MCP-Token": "secret-token-123",
                        "Origin": bad_origin,
                    },
                    body,
                )
                self.assertEqual(status, 403)

        status, data = self._raw_post(
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
        self.assertTrue(raw_resp.startswith("HTTP/1.0 413") or raw_resp.startswith("HTTP/1.1 413"), raw_resp)

    def test_rejects_negative_zero_missing_nonint_content_length_with_400(self):
        for cl_header in ("Content-Length: -5\r\n", "Content-Length: 0\r\n", "Content-Length: abc\r\n", ""):
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
                self.assertTrue(raw_resp.startswith("HTTP/1.0 400") or raw_resp.startswith("HTTP/1.1 400"), raw_resp)

    def test_http_non_dict_json_returns_32600(self):
        body = b"[1, 2, 3]"
        status, data = self._raw_post(
            {"Content-Type": "application/json", "Content-Length": str(len(body)), "X-MCP-Token": "secret-token-123"},
            body,
        )
        self.assertEqual(status, 200)
        self.assertEqual(data["error"]["code"], -32600)

    def test_main_http_mode_fails_fast_without_mcp_token(self):
        with patch.dict(os.environ, {"MCP_TOKEN": "   "}), patch("sys.argv", ["server.py", "--port", "0"]):
            with self.assertRaises(SystemExit) as ctx:
                mcp_server.main()
            self.assertNotEqual(ctx.exception.code, 0)


if __name__ == "__main__":
    unittest.main()
