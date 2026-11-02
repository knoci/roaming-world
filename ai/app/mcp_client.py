import json
import os
import urllib.request


class MCPClient:
    def __init__(self, url: str | None = None, token: str | None = None):
        self.url = url or os.getenv("MCP_URL", "http://127.0.0.1:18008/mcp")
        self.token = token or os.getenv("MCP_TOKEN", "")
        self._next_id = 0

    def _post(self, payload: dict) -> dict:
        req = urllib.request.Request(
            self.url,
            data=json.dumps(payload).encode("utf-8"),
            headers={"Content-Type": "application/json", "X-MCP-Token": self.token},
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=8) as resp:
            return json.loads(resp.read().decode("utf-8"))

    def request(self, method: str, params: dict | None = None) -> dict:
        self._next_id += 1
        payload = {"jsonrpc": "2.0", "id": self._next_id, "method": method}
        if params is not None:
            payload["params"] = params
        res = self._post(payload)
        if "error" in res:
            raise RuntimeError(res["error"].get("message", "mcp error"))
        return res.get("result", {})

    def initialize(self) -> dict:
        return self.request("initialize")

    def list_tools(self) -> list[dict]:
        return self.request("tools/list").get("tools", [])

    def call_tool(self, name: str, arguments: dict) -> list | dict:
        result = self.request("tools/call", {"name": name, "arguments": arguments})
        text = result.get("content", [{}])[0].get("text", "[]")
        return json.loads(text)
