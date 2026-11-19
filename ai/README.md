# AI Engine

最小 RAG 闭环：PostgreSQL + pgvector 检索，返回带引用的答案。

## 接口

```text
GET  /healthz
GET  /readyz
POST /v1/ai/ingest    X-Ingest-Token: <AI_INGEST_TOKEN>
POST /v1/ai/chat      {"query":"成都三天美食行程","top_k":5}
POST /v1/ai/agent     {"query":"成都三天美食行程"}
POST /v1/ai/graph     {"query":"成都三天美食行程"}
POST /v1/ai/multi     {"query":"成都三天美食行程"}
GET  /v1/ai/providers
```

网关通过 `X-AI-Gateway-Token` 调用对话接口，客户端使用 JWT 访问网关。入库和查看模型配置都需要独立的 `X-Ingest-Token`。

## 数据流

```text
articles/scenes/foods/audiobooks
  -> 清洗 + 分块
  -> hash embedding (offline fallback)
  -> pgvector
  -> cosine 检索 top_k
  -> extractive answer + citations
```

## Embedding

当前默认：

```text
AI_EMBEDDING_PROVIDER=hash
```

可选：

```text
AI_EMBEDDING_PROVIDER=openai
EMBEDDING_API_URL=https://api.openai.com/v1/embeddings
EMBEDDING_API_KEY=...
EMBEDDING_MODEL=BAAI/bge-m3
```

维度固定 1024；换模型必须保证输出维度一致。

这是**离线开发用确定性 hash embedding**，不是 BGE-M3，不代表语义检索质量。  
生产应换成 BGE-M3 / 其他 embedding API，并重新 ingest。

## Agent

`/v1/ai/agent` 是确定的工具编排，不是 LangGraph：

```text
extract city/days -> MCP search tools -> planner -> citations
```

它通过 HTTP JSON-RPC 调 mcp-service，返回 answer/citations/tool_calls/plan。  
默认不调用 LLM；配置 `LLM_PROVIDER=openai` 后会用 OpenAI-compatible chat 生成，生成失败自动回退抽取式答案。

`/v1/ai/graph` 用 supervisor -> worker -> reviewer 的确定性状态图，
形状接近 LangGraph。镜像安装 `langgraph` 后走真实 StateGraph；本地未安装时自动走确定性 fallback，并在响应的 backend 字段标明。

## 多模型 Agent

`/v1/ai/multi` 使用 LangGraph StateGraph 编排 planner/reviewer/writer。

`/v1/ai/multi` 使用 Qwen planner、DeepSeek reviewer、MiMo writer；key 只从环境变量读取。详见 [docs/AI-PROVIDERS.md](../docs/AI-PROVIDERS.md)。

## LLM

当前默认是**抽取式回答**，不调用外部 LLM。  
这样没有 API key 也能跑通检索和引用链路。  
后续接 LLM 时只替换 answer 层，不动检索和引用。

## 本地

```powershell
docker compose ps ai mcp gateway
curl http://localhost:18009/healthz
```

完成 [演示数据初始化](../docs/AI-RUNBOOK.md) 后，可以用 `scripts/ai-demo.ps1 -Kubernetes:$false` 在 Compose 环境跑入库和 JWT 调用。

