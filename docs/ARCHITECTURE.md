# 架构

## 总览

```text
Client
  -> K8s Ingress (TLS + SSE proxy-buffering=off)
       -> 7 个 Go/Kratos 业务服务 (go.work + pkg/)
            -> PostgreSQL (版本化迁移 001/002 + 连接池)
            -> Redis (缓存 + 验证码 Lua + JWT Refresh/吊销 + 限流)
            -> Kafka (Outbox + Hash 分区 + DLQ)
            -> Meilisearch
       -> AI Gateway (JWT + RPM/TPM 限流 + 熔断 + 原生 SSE 代理)
            -> Async AI Engine (FastAPI + psycopg_pool + httpx)
                 -> Hybrid RAG (pgvector HNSW + PG FTS + RRF + Rerank)
                 -> LangGraph Multi-Agent (并行星扇出 + 有界自纠错 + HITL)
                 -> MCP Server (13 Tools / Resources / Prompts) -> 7 Business APIs
```

## 服务边界

| 模块 | 职责 | 数据 |
|---|---|---|
| pkg | 共享 JWT/吊销、Nacos 客户端、DB 连接池、`/healthz`/`/readyz` | 无状态 |
| user | 账号、短效 JWT + Refresh/Logout、Redis Lua 验证码、关注、通知 | users、follows、notifications、Redis |
| scene | 景点内容、版本化缓存 + singleflight、Meilisearch 增量同步 | scenes、Redis、Meilisearch |
| food | 美食内容、计数偏移随机查询 | foods、Redis |
| comment | 评论、回复（窗口函数分页）、点赞、举报、事务 Outbox 发布 | comments、comment_reports、comment_outbox |
| article | 文章、Feed、生命周期乐观锁、点赞收藏、Kafka 消费与 DLQ | articles、likes、favorites、Kafka DLQ |
| audiobook | 有声书与章节、cache-first 缓存 | audiobooks、audiobook_details、Redis |
| plan | 旅行计划与每日条目（事务行锁防重叠） | travel_plans、travel_plan_days、travel_plan_items |
| gateway | AI 接口 JWT 鉴权、RPM/TPM 限流、熔断与 SSE 流式代理 | Redis / 内存限流状态 |
| ai | Async Hybrid RAG、增量索引、LangGraph Multi-Agent、HITL | ai_documents、ai_chunks |
| mcp | 标准 MCP Server（12 只读工具 + 1 HITL 写工具 + Resources + Prompts） | 无状态 |

## 核心数据流

### 内容与事件一致性

- HTTP/gRPC 请求经过 `validate.Validator()` 与统一 `pkg/auth` 鉴权中间件；
- 业务数据写入 PostgreSQL（通过 `001_init.sql` + `002_phase2_schema_and_indexes.sql` 版本化管理，生产默认关闭 `AutoMigrate`）；
- `comment` 在增删/举报同一事务写入 `comment_outbox`，由后台工作协程事务外发送 Kafka（`Hash` 分区保序、租约防并发、超重试转 `dead_letter`），`article` 消费端带退避重试与 `<topic>-dlq` 死信队列；
- 热点内容使用 Redis 缓存并配合 `singleflight` 与空值短 TTL 防击穿/穿透；定时对账脚本 `reconcile_counts.sql` 按 500 行短事务分批校准。

### AI 与 MCP

```text
/v1/ai/ingest (增量内容哈希 diff)
  -> 分块并注入标题/地点上下文 -> 事务外批量 Embedding -> ai_chunks (pgvector + tsvector)

/v1/ai/chat (Hybrid RAG)
  -> pgvector HNSW + PG FTS -> RRF 融合 -> Rerank & 文档去重 -> 引用落地校验

/v1/ai/stream (Async Multi-Agent + HITL)
  -> router
  -> 并行专家扇出 (scenic_researcher / food_researcher / community_researcher via MCP + Hybrid RAG)
  -> planner (结构化行程) <-> verifier (规则 + JSON 审核, max_revisions=2)
  -> approval_gate (HITL 暂停) -> /v1/ai/threads/{id}/resume -> MCP save_travel_plan_draft (Idempotency-Key)
```

## 安全与部署约定

- 短效 Access Token（默认 30m）+ `jti`/`token_version` Redis 吊销 + 单次消费 Refresh Token 轮换；
- Nacos、Redis、PostgreSQL、Meilisearch 与内部服务间 Token 均从环境变量/Secret 注入，禁止明文弱口令入库；
- 全量容器以非 `root` 用户（UID `10001`）运行，K8s 启用 `readOnlyRootFilesystem`、最小 `NetworkPolicy` 网格隔离与多副本 `PDB`。
