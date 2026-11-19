# MCP Server

自建 MCP Server，把站内内容 API 暴露为只读工具。

## 传输

- HTTP JSON-RPC：`POST /mcp`
- stdio：`python server.py --stdio`
- 健康检查：`GET /healthz`

这是核心 MCP 方法的精简实现：`initialize`、`tools/list`、`tools/call`、`ping`。  
没有实现完整 Streamable HTTP/SSE、OAuth、resources/prompts。

## 工具

| 工具 | 说明 |
|---|---|
| search_scenes | 按关键词检索景点 |
| search_foods | 按关键词检索美食 |
| search_articles | 检索已发布文章 |
| get_scene | 按 sid 取景点 |
| get_article | 按 aid 取文章 |

全部只读，不提供写工具。

## 配置

```text
SCENE_API=http://scene:8000
FOOD_API=http://food:8000
ARTICLE_API=http://article:8000
MCP_TOKEN=<与 AI Engine 共享的内部令牌>
```

HTTP 请求必须带 `X-MCP-Token`。未配置 `MCP_TOKEN` 时拒绝工具调用。

```text
X-MCP-Token: <MCP_TOKEN>
```

## 本地 HTTP

```powershell
$env:SCENE_API="http://localhost:18001"
$env:FOOD_API="http://localhost:18002"
$env:ARTICLE_API="http://localhost:18004"
$env:MCP_TOKEN=[guid]::NewGuid().ToString("N")
python mcp/server.py --host 127.0.0.1 --port 18008
```

初始化：

```powershell
Invoke-RestMethod -Method Post http://127.0.0.1:18008/mcp -ContentType "application/json" -Headers @{"X-MCP-Token"=$env:MCP_TOKEN} -Body '{"jsonrpc":"2.0","id":1,"method":"initialize"}'
```

列工具：

```powershell
Invoke-RestMethod -Method Post http://127.0.0.1:18008/mcp -ContentType "application/json" -Headers @{"X-MCP-Token"=$env:MCP_TOKEN} -Body '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
```

调用：

```powershell
Invoke-RestMethod -Method Post http://127.0.0.1:18008/mcp -ContentType "application/json" -Headers @{"X-MCP-Token"=$env:MCP_TOKEN} -Body '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_scenes","arguments":{"query":"成都"}}}'
```

## stdio

Claude Desktop 等客户端可以配置：

```json
{
  "mcpServers": {
    "roaming-world": {
      "command": "python",
      "args": ["C:\\path\\to\\mcp\\server.py", "--stdio"],
      "env": {
        "SCENE_API": "http://localhost:18001",
        "FOOD_API": "http://localhost:18002",
        "ARTICLE_API": "http://localhost:18004"
      }
    }
  }
}
```

