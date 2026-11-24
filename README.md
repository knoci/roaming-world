# Roaming World

生产级旅行内容社区与 **Backend AI Agent** 微服务平台——结合 **Go (Kratos + go.work)** 微服务网格与 **Python (FastAPI + LangGraph + Official MCP SDK + pgvector Hybrid RAG)** 异步多智能体系统。

## 核心特性

- **内容与社区微服务**：景点、美食、文章与视频、有声书、评论/回复/举报、关注与通知、按天旅行计划
- **Agentic Hybrid RAG**：`pgvector` HNSW + PostgreSQL `tsvector` RRF 混合检索、文档级去重重排、低相关拒答、内容哈希增量索引、引用落地校验
- **异步有状态 Multi-Agent**：启动期预编译 LangGraph 编排、`router` + 3 专家并行扇出 + 有界自纠错（`max_revisions=2`）+ HITL 写前审批中断与恢复（`Idempotency-Key` 幂等落库）
- **标准 MCP 服务**：基于官方 MCP 协议规范（Tools / Resources / Prompts、Streamable HTTP + `stdio`），覆盖全站 7 大业务域 13 个工具与摘要投影截断
- **流式 AI Gateway**：原生 SSE 流式代理（支持客户端断连取消传播）、用户级 RPM + 每日 Token 预算双维限流、上游熔断保护
- **生产级云原生基线**：统一 `go.work` + `pkg/` 共享底座、Redis Lua 验证码与短效 JWT + Refresh 吊销、事务 Outbox + Hash 分区 + DLQ、非 `root` 容器、K8s NetworkPolicy / PDB / Pod 级 Prometheus + Grafana LLM Ops 面板

## 系统架构

```text
Client
  ├──► K8s Ingress (TLS + SSE proxy-buffering=off)
  │      ├──► 7 Go/Kratos Services (go.work + pkg/)
  │      │      └──► PostgreSQL + pgvector | Redis | Kafka (Outbox/DLQ) | Meilisearch
  │      └──► AI Gateway (:18009, JWT + RPM/TPM Rate Limit + Circuit Breaker + SSE)
  │             └──► Async AI Engine (FastAPI + psycopg_pool + httpx)
  │                    ├──► Hybrid RAG (pgvector HNSW + FTS + RRF + Rerank)
  │                    ├──► LangGraph Multi-Agent (Parallel Fan-Out + Bounded Revision + HITL)
  │                    └──► MCP Server (13 Tools / Resources / Prompts) ──► 7 Business APIs
```

## 目录结构

```text
roaming-world/
├── go.work / pkg/          # Go Monorepo 共享底座（auth / nacosx / dbx / health）
├── user / scene / food / comment / article / audiobook / plan / gateway
├── ai/                     # Async AI Engine（RAG、Multi-Agent、Evals）
├── mcp/                    # Standard MCP Server（Tools / Resources / Prompts）
├── deploy/                 # SQL 版本化迁移、Nacos 模板、Prometheus 告警与 Grafana 看板
├── k8s/ & k8s-single-node/ # Kustomize 生产加固清单与单节点开发覆盖层
├── scripts/                # 初始化、迁移、部署与冒烟脚本
└── docs/                   # 架构与可观测性文档
```

## 快速开始

### Docker Compose

```powershell
# 1. 生成 JWT 密钥、服务间 Token 及基础设施随机密码写入 .env
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\configure-internal-tokens.ps1

# 2. 构建并启动全部服务
docker compose up -d --build

# 3. 验证就绪探针
curl.exe http://localhost:18000/readyz
curl.exe http://localhost:18009/readyz
```

### Kubernetes

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\k8s-up.ps1
```

## AI 接口与模型配置

AI Engine 支持零外部 Key 离线降级运行；配置环境变量后启用在线模型与稠密向量检索：

| 环境变量 | 用途 | 默认值 / 说明 |
|---|---|---|
| `QWEN_API_KEY` / `QWEN_BASE_URL` / `QWEN_MODEL` | Planner 模型 | 兼容 OpenAI 协议接口 |
| `DEEPSEEK_API_KEY` / `DEEPSEEK_BASE_URL` / `DEEPSEEK_MODEL` | Router / Verifier 模型 | 兼容 OpenAI 协议接口 |
| `XIAOMI_API_KEY` / `XIAOMI_BASE_URL` / `XIAOMI_MODEL` | Writer 模型 | 兼容 OpenAI 协议接口 |
| `AI_EMBEDDING_PROVIDER` / `AI_EMBEDDING_API_*` | Embedding 服务 | `hash`（离线）或 `openai` |
| `AI_RERANK_API_URL` / `AI_RERANK_API_KEY` | Reranker（可选） | 未配置时使用本地融合精排 |

核心接口（经由 `gateway :18009` 对外暴露）：

```text
POST /v1/ai/chat                    Hybrid RAG 问答与引用落地
POST /v1/ai/agent                   MCP Agent 问答
POST /v1/ai/graph                   LangGraph 状态图
POST /v1/ai/multi                   多 Agent 行程生成
POST /v1/ai/stream                  SSE 流式推送（stage/tool_call/citation/token/approval_required/done）
POST /v1/ai/threads/{id}/resume     HITL 审批恢复并幂等写入行程草稿
```

## 测试与离线评测

```bash
# 运行 30 用例离线 RAG & Agent 回归评测
python -m ai.evals.run_eval

# 运行 Python 单测
python -m unittest discover -s mcp/tests -v
python -m unittest discover -s ai/tests -v
```

| 评测指标 | 实测基线 | CI 门禁阈值 | 状态 |
|---|---|---|---|
| `recall_at_5` | `1.0000` | `0.8500` | PASS |
| `mrr` | `0.8694` | `0.7000` | PASS |
| `citation_precision` | `1.0000` | `0.9500` | PASS |
| `abstention_accuracy` | `1.0000` | `0.8000` | PASS |
| `injection_defense_rate` | `1.0000` | `1.0000` | PASS |
| `tool_selection_accuracy` | `1.0000` | `0.8500` | PASS |

## 文档

- [系统架构](docs/ARCHITECTURE.md)
- [可观测性与监控面板](docs/OBSERVABILITY.md)

## License

[MIT](LICENSE)
