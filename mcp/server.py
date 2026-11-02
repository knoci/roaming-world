"""Minimal MCP JSON-RPC server for Roaming World.

Tools are read-only and call the existing HTTP APIs. This implements the
core MCP methods (initialize, tools/list, tools/call); it is intentionally
small and does not claim to implement every transport feature.
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Callable

PROTOCOL_VERSION = "2024-11-05"


def _env(name: str, default: str) -> str:
    return os.getenv(name, default).rstrip("/")


TOOLS = [
    {
        "name": "search_scenes",
        "description": "按关键词检索景点，返回名称、地点和简介。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string"},
                "limit": {"type": "integer", "minimum": 1, "maximum": 20},
            },
            "required": ["query"],
        },
    },
    {
        "name": "search_foods",
        "description": "按关键词检索美食，返回名称、地点和简介。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string"},
                "limit": {"type": "integer", "minimum": 1, "maximum": 20},
            },
            "required": ["query"],
        },
    },
    {
        "name": "search_articles",
        "description": "检索已发布文章，返回标题和摘要。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string"},
                "limit": {"type": "integer", "minimum": 1, "maximum": 20},
            },
            "required": ["query"],
        },
    },
    {
        "name": "get_scene",
        "description": "按 sid 获取单个景点详情。",
        "inputSchema": {
            "type": "object",
            "properties": {"sid": {"type": "string"}},
            "required": ["sid"],
        },
    },
    {
        "name": "get_article",
        "description": "按 aid 获取单篇文章详情。",
        "inputSchema": {
            "type": "object",
            "properties": {"aid": {"type": "string"}},
            "required": ["aid"],
        },
    },
]


class MCPHandler:
    def __init__(self, fetch_json: Callable[[str], Any] | None = None):
        self.scene_api = _env("SCENE_API", "http://127.0.0.1:18001")
        self.food_api = _env("FOOD_API", "http://127.0.0.1:18002")
        self.article_api = _env("ARTICLE_API", "http://127.0.0.1:18004")
        self.fetch_json = fetch_json or self._http_json

    @staticmethod
    def _http_json(url: str) -> Any:
        with urllib.request.urlopen(url, timeout=5) as resp:
            return json.loads(resp.read().decode("utf-8"))

    def _get(self, url: str) -> Any:
        return self.fetch_json(url)

    @staticmethod
    def _matches(query: str, *parts: str) -> bool:
        q = query.lower().strip()
        return q in " ".join(p or "" for p in parts).lower()

    def _search(self, base: str, path: str, query: str, limit: int) -> list[dict]:
        data = self._get(base + path)
        items = data.get("scenes") or data.get("foods") or data.get("articles") or []
        out = []
        for item in items:
            if self._matches(query, item.get("name", ""), item.get("describe", ""),
                             item.get("content", ""), item.get("location", "")):
                out.append(item)
            if len(out) >= limit:
                break
        return out

    def call_tool(self, name: str, args: dict) -> Any:
        limit = max(1, min(int(args.get("limit", 5)), 20))
        if name == "search_scenes":
            return self._search(self.scene_api, "/v1/scene?page=1&pagesize=50", args["query"], limit)
        if name == "search_foods":
            return self._search(self.food_api, "/v1/food", args["query"], limit)
        if name == "search_articles":
            return self._search(self.article_api, "/v1/article?page=1&limit=50&way=time", args["query"], limit)
        if name == "get_scene":
            return self._get(f"{self.scene_api}/v1/scene/{urllib.parse.quote(args['sid'])}")
        if name == "get_article":
            return self._get(f"{self.article_api}/v1/article/{urllib.parse.quote(args['aid'])}")
        raise ValueError(f"unknown tool: {name}")

    def handle(self, req: dict) -> dict | None:
        if req.get("jsonrpc") != "2.0":
            return self._error(req.get("id"), -32600, "invalid jsonrpc version")
        method = req.get("method")
        rid = req.get("id")

        if method == "initialize":
            return self._result(rid, {
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "roaming-world-mcp", "version": "0.1.0"},
            })
        if method in ("notifications/initialized", "notifications/cancelled"):
            return None
        if method == "ping":
            return self._result(rid, {})
        if method == "tools/list":
            return self._result(rid, {"tools": TOOLS})
        if method == "tools/call":
            params = req.get("params") or {}
            name = params.get("name")
            args = params.get("arguments") or {}
            try:
                value = self.call_tool(name, args)
            except Exception as exc:
                return self._error(rid, -32603, str(exc))
            return self._result(rid, {
                "content": [{"type": "text", "text": json.dumps(value, ensure_ascii=False)}],
                "isError": False,
            })
        return self._error(rid, -32601, f"method not found: {method}")

    @staticmethod
    def _result(rid, result):
        return {"jsonrpc": "2.0", "id": rid, "result": result}

    @staticmethod
    def _error(rid, code, message):
        return {"jsonrpc": "2.0", "id": rid, "error": {"code": code, "message": message}}


def _check_token(headers) -> bool:
    expected = os.getenv("MCP_TOKEN", "")
    return not expected or headers.get("X-MCP-Token") == expected


def make_http_server(host: str, port: int):
    handler = MCPHandler()

    class HTTPHandler(BaseHTTPRequestHandler):
        def _json(self, code: int, body: dict):
            raw = json.dumps(body, ensure_ascii=False).encode("utf-8")
            self.send_response(code)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def do_GET(self):
            if self.path == "/healthz":
                self._json(200, {"status": "ok"})
            else:
                self._json(404, {"error": "not found"})

        def do_POST(self):
            if self.path != "/mcp":
                self._json(404, {"error": "not found"})
                return
            if not _check_token(self.headers):
                self._json(401, {"error": "unauthorized"})
                return
            try:
                length = int(self.headers.get("Content-Length", "0"))
                req = json.loads(self.rfile.read(length))
                res = handler.handle(req)
            except Exception as exc:
                self._json(400, {"jsonrpc": "2.0", "id": None,
                                 "error": {"code": -32700, "message": str(exc)}})
                return
            if res is None:
                self.send_response(204)
                self.end_headers()
                return
            self._json(200, res)

        def log_message(self, fmt, *args):
            sys.stderr.write("mcp: " + (fmt % args) + "\n")

    return ThreadingHTTPServer((host, port), HTTPHandler)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--stdio", action="store_true")
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=8000)
    args = parser.parse_args()

    if args.stdio:
        handler = MCPHandler()
        for line in sys.stdin:
            line = line.strip()
            if not line:
                continue
            try:
                res = handler.handle(json.loads(line))
            except Exception as exc:
                res = {"jsonrpc": "2.0", "id": None,
                       "error": {"code": -32700, "message": str(exc)}}
            if res is not None:
                sys.stdout.write(json.dumps(res, ensure_ascii=False) + "\n")
                sys.stdout.flush()
        return

    server = make_http_server(args.host, args.port)
    server.serve_forever()


if __name__ == "__main__":
    main()
