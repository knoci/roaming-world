# Roaming World

旅行内容与社区平台，提供内容发布、互动、行程规划和 AI 旅行助手。

## 功能

- 景点、美食、文章、有声书
- 评论、回复、点赞、收藏、举报、关注、通知
- 按天创建旅行计划
- pgvector RAG 问答与引用
- MCP 工具调用与多模型行程生成
- Prometheus 指标与 Grafana 面板

## 架构

```text
Client -> Kratos HTTP APIs -> PostgreSQL / Redis / Kafka / Meilisearch

AI Gateway -> AI Engine
               -> pgvector (RAG)
               -> MCP Server -> Kratos content APIs
               -> Qwen / DeepSeek / MiMo (LangGraph)
```

| 模块 | 说明 | 端口 |
|---|---|---|
| user | 账号、JWT、关注、通知 | 18000 |
| scene | 景点 | 18001 |
| food | 美食 | 18002 |
| comment | 评论与互动 | 18003 |
| article | 文章、Feed、点赞收藏 | 18004 |
| audiobook | 有声书 | 18005 |
| plan | 旅行计划 | 18006 |
| ai | RAG / Agent Engine | 18007 |
| mcp | MCP 工具服务 | 18008 |
| gateway | AI 网关 | 18009 |

## 快速开始

### Docker Compose

```bash
docker compose up -d --build
curl http://localhost:18000/healthz
curl http://localhost:18007/healthz
```

### Kubernetes

需要 Docker Desktop、minikube、kubectl：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\k8s-up.ps1
```

单节点环境用于开发与演示，不是生产高可用部署。

## AI

默认使用 hash embedding + pgvector 的轻量 RAG，无需模型 key。

多模型协作使用：

```text
Qwen planner -> DeepSeek reviewer -> MiMo writer
```

配置方式见 [docs/AI-PROVIDERS.md](docs/AI-PROVIDERS.md)。key 只从环境变量或本地 K8s Secret 读取。

```text
POST /v1/ai/chat    RAG 问答
POST /v1/ai/multi   多 Agent 行程生成
POST /v1/ai/graph   LangGraph 状态图
```

## 技术栈

Go、Kratos、Wire、Python、FastAPI、PostgreSQL、pgvector、Redis、Kafka、Meilisearch、Nacos、Docker、Kubernetes、Prometheus、Grafana。

## 文档

- [架构](docs/ARCHITECTURE.md)
- [AI 多模型配置](docs/AI-PROVIDERS.md)
- [AI 运行手册](docs/AI-RUNBOOK.md)
- [可观测性](docs/OBSERVABILITY.md)
- [日志](docs/LOGGING.md)

## License

MIT
