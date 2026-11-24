# Observability

## 探针与 Metrics

所有业务与 AI 服务均区分存活与就绪探针，并暴露 Prometheus 指标端点：

- `GET /healthz`：进程存活探针（`async` / 非阻塞）
- `GET /readyz`：依赖就绪探针（验证 PostgreSQL / Redis / 关键配置可用性）
- `GET /metrics`：Prometheus exposition format

### 指标体系

- **Go 微服务**：`roaming_http_requests_total`、`roaming_http_duration_microseconds_total`
- **AI Engine**（[ai/app/metrics.py](file:///c:/Users/11484/Desktop/roaming-world/ai/app/metrics.py)）：
  - `ai_http_requests_total{route,method,status}`、`ai_http_request_duration_seconds`（Histogram）
  - `ai_llm_requests_total{provider,role,status}`、`ai_llm_tokens_total{provider,type}`
  - `ai_rag_top1_score`、`ai_agent_revisions_total`
- **MCP Server**（[mcp/server.py](file:///c:/Users/11484/Desktop/roaming-world/mcp/server.py)）：
  - `roaming_mcp_tool_calls_total{tool,status}`、`roaming_mcp_tool_duration_seconds`（Histogram）

## Prometheus 与告警规则

Docker Compose：

- `prometheus` (`http://localhost:9090`)：配置见 [deploy/prometheus/prometheus.yml](file:///c:/Users/11484/Desktop/roaming-world/deploy/prometheus/prometheus.yml)，抓取全站 10 个服务（含 `ai`、`mcp`、`gateway`），挂载持久化卷 `prometheus_data`。
- 告警规则见 [deploy/prometheus/alerts.yml](file:///c:/Users/11484/Desktop/roaming-world/deploy/prometheus/alerts.yml)：覆盖 `ServiceDown`、`AIHigh5xxRate`、`AILLMHighLatencyP95`、`MCPToolErrors`。

Kubernetes（[k8s/observability.yaml](file:///c:/Users/11484/Desktop/roaming-world/k8s/observability.yaml)）：

- 通过 `ServiceAccount` + `Role` 启用 `kubernetes_sd_configs (role: pod)` 按 Pod 独立抓取，避免 ClusterIP 轮询造成多副本 Counter 失真；同步内嵌 SLO 告警规则与 Prometheus 健康探针。

## Grafana 预置面板

Docker Compose 已预置 Grafana（`http://localhost:3000`，默认账号 `admin`，密码由 `.env` 中 `GF_SECURITY_ADMIN_PASSWORD` 指定）并挂载持久化卷 `grafana_data`：

- 数据源配置：[deploy/grafana/provisioning/datasources/prometheus.yml](file:///c:/Users/11484/Desktop/roaming-world/deploy/grafana/provisioning/datasources/prometheus.yml)
- 看板自动加载：[deploy/grafana/provisioning/dashboards/dashboards.yml](file:///c:/Users/11484/Desktop/roaming-world/deploy/grafana/provisioning/dashboards/dashboards.yml)
- LLM / Agent / 微服务运维看板：[deploy/grafana/provisioning/dashboards/roaming-world-llm-ops.json](file:///c:/Users/11484/Desktop/roaming-world/deploy/grafana/provisioning/dashboards/roaming-world-llm-ops.json)（覆盖 HTTP QPS、AI P95 延迟与 5xx、LLM Provider/Role 调用量、Token 消耗速率、RAG Top-1 分数与自纠错次数、MCP 工具调用与 P95 延迟）。
