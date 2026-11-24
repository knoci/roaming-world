import asyncio
import json
import os
import urllib.request
from typing import Any

try:
    import httpx
except ImportError:  # stdlib-only local test environments
    httpx = None  # type: ignore[assignment]


_FORWARDED_HEADERS = ("X-Request-ID", "traceparent", "X-User-ID", "Idempotency-Key")


class MCPClient:
    def __init__(self, url: str | None = None, token: str | None = None, timeout: float = 8.0):
        self.url = url or os.getenv("MCP_URL", "http://127.0.0.1:18008/mcp")
        self.token = token or os.getenv("MCP_TOKEN", "")
        self.timeout = timeout
        self._next_id = 0

    def _build_headers(self, extra: dict[str, str] | None = None) -> dict[str, str]:
        headers = {
            "Content-Type": "application/json",
            "X-MCP-Token": self.token,
        }
        if extra:
            for k, v in extra.items():
                if v is not None and str(v).strip() != "":
                    headers[k] = str(v)
        return headers

    def _post(self, payload: dict, headers: dict[str, str] | None = None) -> dict:
        req = urllib.request.Request(
            self.url,
            data=json.dumps(payload).encode("utf-8"),
            headers=self._build_headers(headers),
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=self.timeout) as resp:
            return json.loads(resp.read().decode("utf-8"))

    def request(self, method: str, params: dict | None = None, *, headers: dict[str, str] | None = None) -> dict:
        self._next_id += 1
        payload = {"jsonrpc": "2.0", "id": self._next_id, "method": method}
        if params is not None:
            payload["params"] = params
        res = self._post(payload, headers=headers)
        if "error" in res:
            raise RuntimeError(res["error"].get("message", "mcp error"))
        return res.get("result", {})

    def initialize(self) -> dict:
        return self.request("initialize")

    def list_tools(self) -> list[dict]:
        return self.request("tools/list").get("tools", [])

    @staticmethod
    def _parse_tool_result(result: dict) -> list | dict:
        content = result.get("content") or []
        text = content[0].get("text", "[]") if content and isinstance(content[0], dict) else "[]"
        if result.get("isError"):
            raise RuntimeError(text or "mcp tool error")
        return json.loads(text)

    def call_tool(
        self,
        name: str,
        arguments: dict,
        *,
        headers: dict[str, str] | None = None,
    ) -> list | dict:
        if headers:
            result = self.request("tools/call", {"name": name, "arguments": arguments}, headers=headers)
        else:
            result = self.request("tools/call", {"name": name, "arguments": arguments})
        return self._parse_tool_result(result)

    async def arequest(
        self,
        method: str,
        params: dict | None = None,
        *,
        headers: dict[str, str] | None = None,
        client: Any = None,
    ) -> dict:
        self._next_id += 1
        payload = {"jsonrpc": "2.0", "id": self._next_id, "method": method}
        if params is not None:
            payload["params"] = params

        if client is None and httpx is None:
            return await asyncio.to_thread(self.request, method, params, headers=headers)

        req_headers = self._build_headers(headers)
        if client is not None:
            resp = await client.post(self.url, json=payload, headers=req_headers, timeout=self.timeout)
            resp.raise_for_status()
            res = resp.json()
        else:
            async with httpx.AsyncClient(timeout=self.timeout) as ac:
                resp = await ac.post(self.url, json=payload, headers=req_headers, timeout=self.timeout)
                resp.raise_for_status()
                res = resp.json()

        if "error" in res:
            raise RuntimeError(res["error"].get("message", "mcp error"))
        return res.get("result", {})

    async def acall_tool(
        self,
        name: str,
        arguments: dict,
        *,
        headers: dict[str, str] | None = None,
        client: Any = None,
    ) -> list | dict:
        forwarded = None
        if headers:
            forwarded = {
                k: v for k, v in headers.items()
                if v is not None and str(v).strip() != ""
            }
        result = await self.arequest(
            "tools/call",
            {"name": name, "arguments": arguments},
            headers=forwarded,
            client=client,
        )
        return self._parse_tool_result(result)
