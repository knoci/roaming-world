"""Minimal MCP JSON-RPC server for Roaming World.

Tools are read-only and call the existing HTTP APIs. This implements the
core MCP methods (initialize, tools/list, tools/call); it is intentionally
small and does not claim to implement every transport feature.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import secrets
import sys
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Callable

PROTOCOL_VERSION = "2024-11-05"
MAX_BODY_BYTES = 1_048_576  # 1 MiB
MAX_QUERY_LEN = 500
EXCERPT_MAX_LEN = 250
ID_PATTERN = re.compile(r"^[A-Za-z0-9_-]{1,64}\Z")


class InvalidParamsError(ValueError):
    """Raised when MCP tool parameters fail validation."""


def _env(name: str, default: str) -> str:
    return os.getenv(name, default).rstrip("/")


def _validate_base_url(name: str, url: str) -> str:
    cleaned = (url or "").strip().rstrip("/")
    parsed = urllib.parse.urlparse(cleaned)
    if parsed.scheme not in ("http", "https") or not parsed.netloc:
        raise ValueError(f"invalid {name} url: scheme must be http or https with non-empty host")
    return cleaned


def _validate_id(name: str, value: Any) -> str:
    if not isinstance(value, str) or not ID_PATTERN.fullmatch(value):
        raise InvalidParamsError(f"invalid {name}")
    return value


def _validate_query(value: Any) -> str:
    if not isinstance(value, str):
        raise InvalidParamsError("query must be a non-empty string")
    if len(value) > MAX_QUERY_LEN:
        raise InvalidParamsError(f"query length must be <= {MAX_QUERY_LEN}")
    q = value.strip()
    if not q:
        raise InvalidParamsError("query must be a non-empty string")
    return q


def _validate_limit(args: dict, default: int = 5) -> int:
    if "limit" not in args or args["limit"] is None:
        return default
    raw = args["limit"]
    if isinstance(raw, bool) or not isinstance(raw, int):
        raise InvalidParamsError("limit must be an integer between 1 and 20")
    if raw < 1 or raw > 20:
        raise InvalidParamsError("limit must be an integer between 1 and 20")
    return raw


def _truncate(text: Any, max_len: int = EXCERPT_MAX_LEN) -> str:
    s = str(text or "").strip()
    if len(s) <= max_len:
        return s
    return s[: max_len - 3] + "..." if max_len > 3 else s[:max_len]


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
        self.scene_api = _validate_base_url("SCENE_API", _env("SCENE_API", "http://127.0.0.1:18001"))
        self.food_api = _validate_base_url("FOOD_API", _env("FOOD_API", "http://127.0.0.1:18002"))
        self.article_api = _validate_base_url("ARTICLE_API", _env("ARTICLE_API", "http://127.0.0.1:18004"))
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
        return q in " ".join(str(p or "") for p in parts).lower()

    @staticmethod
    def _project_scene(item: dict) -> dict:
        return {
            "sid": item.get("sid", ""),
            "name": item.get("name", ""),
            "location": item.get("location", ""),
            "describe": _truncate(item.get("describe") or item.get("content") or "", EXCERPT_MAX_LEN),
        }

    @staticmethod
    def _project_food(item: dict) -> dict:
        return {
            "fid": item.get("fid", ""),
            "name": item.get("name", ""),
            "location": item.get("location", ""),
            "describe": _truncate(item.get("describe") or item.get("recipe") or item.get("content") or "", EXCERPT_MAX_LEN),
        }

    @staticmethod
    def _project_article(item: dict) -> dict:
        return {
            "aid": item.get("aid", ""),
            "title": item.get("title", ""),
            "name": item.get("name", ""),
            "likes": item.get("likes", 0),
            "publishedAt": item.get("publishedAt", item.get("published_at", "")),
            "excerpt": _truncate(item.get("excerpt") or item.get("content") or item.get("describe") or "", EXCERPT_MAX_LEN),
        }

    def _search_scenes(self, query: str, limit: int) -> list[dict]:
        encoded = urllib.parse.quote(query, safe="")
        data = self._get(f"{self.scene_api}/v1/scene/search?keyword={encoded}&page=1&pagesize={limit}")
        items = (data.get("scenes") or []) if isinstance(data, dict) else []
        if items:
            return [self._project_scene(item) for item in items[:limit] if isinstance(item, dict)]
        # Fallback to paginated list + local match when backend search returns no items.
        fallback_data = self._get(f"{self.scene_api}/v1/scene?page=1&pagesize=50")
        fallback_items = (fallback_data.get("scenes") or []) if isinstance(fallback_data, dict) else []
        out = []
        for item in fallback_items:
            if not isinstance(item, dict):
                continue
            if self._matches(query, item.get("name", ""), item.get("describe", ""),
                             item.get("content", ""), item.get("location", "")):
                out.append(self._project_scene(item))
            if len(out) >= limit:
                break
        return out

    def _search_foods(self, query: str, limit: int) -> list[dict]:
        data = self._get(f"{self.food_api}/v1/food?page=1&limit=50")
        items = (data.get("foods") or []) if isinstance(data, dict) else []
        out = []
        for item in items:
            if not isinstance(item, dict):
                continue
            if self._matches(query, item.get("name", ""), item.get("describe", ""),
                             item.get("recipe", ""), item.get("content", ""), item.get("location", "")):
                out.append(self._project_food(item))
            if len(out) >= limit:
                break
        return out

    def _search_articles(self, query: str, limit: int) -> list[dict]:
        data = self._get(f"{self.article_api}/v1/article?page=1&limit=50&way=time")
        items = (data.get("articles") or []) if isinstance(data, dict) else []
        out = []
        for item in items:
            if not isinstance(item, dict):
                continue
            if self._matches(query, item.get("title", ""), item.get("name", ""),
                             item.get("describe", ""), item.get("content", ""), item.get("location", "")):
                out.append(self._project_article(item))
            if len(out) >= limit:
                break
        return out

    def call_tool(self, name: str, args: dict) -> Any:
        if not isinstance(args, dict):
            raise InvalidParamsError("arguments must be an object")
        if name == "search_scenes":
            query = _validate_query(args.get("query"))
            limit = _validate_limit(args)
            return self._search_scenes(query, limit)
        if name == "search_foods":
            query = _validate_query(args.get("query"))
            limit = _validate_limit(args)
            return self._search_foods(query, limit)
        if name == "search_articles":
            query = _validate_query(args.get("query"))
            limit = _validate_limit(args)
            return self._search_articles(query, limit)
        if name == "get_scene":
            sid = _validate_id("sid", args.get("sid"))
            return self._get(f"{self.scene_api}/v1/scene/{urllib.parse.quote(sid, safe='')}")
        if name == "get_article":
            aid = _validate_id("aid", args.get("aid"))
            return self._get(f"{self.article_api}/v1/article/{urllib.parse.quote(aid, safe='')}")
        raise InvalidParamsError(f"unknown tool: {name}")

    def handle(self, req: Any) -> dict | None:
        if not isinstance(req, dict):
            return self._error(None, -32600, "invalid request")
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
            params = req.get("params")
            if not isinstance(params, dict):
                return self._error(rid, -32602, "invalid params")
            name = params.get("name")
            if not isinstance(name, str) or not name:
                return self._error(rid, -32602, "invalid params: tool name is required")
            args = params.get("arguments")
            if args is None:
                args = {}
            if not isinstance(args, dict):
                return self._error(rid, -32602, "invalid params: arguments must be an object")
            try:
                value = self.call_tool(name, args)
            except InvalidParamsError as exc:
                return self._error(rid, -32602, str(exc))
            except Exception as exc:
                sys.stderr.write(f"mcp: tool {name} failed: {exc}\n")
                return self._result(rid, {
                    "content": [{"type": "text", "text": f"tool execution failed: {name}"}],
                    "isError": True,
                })
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


def _is_origin_allowed(origin: str | None) -> bool:
    if not origin:
        return True
    cleaned = origin.strip().rstrip("/")
    configured = os.getenv("MCP_ALLOWED_ORIGINS", "").strip()
    if configured:
        allowed = {o.strip().rstrip("/") for o in configured.split(",") if o.strip()}
        return cleaned in allowed
    parsed = urllib.parse.urlparse(cleaned)
    if parsed.scheme not in ("http", "https"):
        return False
    if parsed.hostname not in ("localhost", "127.0.0.1", "::1"):
        return False
    if parsed.path not in ("", "/") or parsed.query or parsed.fragment or parsed.username or parsed.password:
        return False
    return True


def _check_token(headers) -> bool:
    expected = os.getenv("MCP_TOKEN", "").strip()
    if not expected:
        return False
    provided = (headers.get("X-MCP-Token") or "").strip()
    if not provided:
        auth = (headers.get("Authorization") or "").strip()
        if auth.lower().startswith("bearer "):
            provided = auth[7:].strip()
    return bool(provided and secrets.compare_digest(provided.encode("utf-8"), expected.encode("utf-8")))


def make_http_server(host: str, port: int, handler: MCPHandler | None = None):
    mcp_handler = handler or MCPHandler()

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
            if not _is_origin_allowed(self.headers.get("Origin")):
                self._json(403, {"error": "forbidden origin"})
                return
            if not _check_token(self.headers):
                self._json(401, {"error": "unauthorized"})
                return
            raw_length = self.headers.get("Content-Length")
            if raw_length is None:
                self._json(400, {"error": "missing content-length"})
                return
            try:
                length = int(raw_length)
            except ValueError:
                self._json(400, {"error": "invalid content-length"})
                return
            if length <= 0:
                self._json(400, {"error": "invalid content-length"})
                return
            if length > MAX_BODY_BYTES:
                self._json(413, {"error": "payload too large"})
                return
            try:
                raw_body = self.rfile.read(length)
                req = json.loads(raw_body.decode("utf-8"))
            except Exception:
                self._json(400, {"jsonrpc": "2.0", "id": None,
                                 "error": {"code": -32700, "message": "parse error"}})
                return
            res = mcp_handler.handle(req)
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
                req = json.loads(line)
            except Exception:
                res = {"jsonrpc": "2.0", "id": None,
                       "error": {"code": -32700, "message": "parse error"}}
            else:
                res = handler.handle(req)
            if res is not None:
                sys.stdout.write(json.dumps(res, ensure_ascii=False) + "\n")
                sys.stdout.flush()
        return

    if not os.getenv("MCP_TOKEN", "").strip():
        sys.stderr.write("mcp: MCP_TOKEN must be set when running in HTTP mode\n")
        sys.exit(1)

    server = make_http_server(args.host, args.port)
    server.serve_forever()


if __name__ == "__main__":
    main()
