# AI Gateway

Kratos HTTP/gRPC 网关，负责：

- 校验 JWT；
- 把请求代理到 Python AI Engine；
- 不直接生成答案。

## 接口

```text
POST /v1/ai/chat
POST /v1/ai/agent
POST /v1/ai/graph
POST /v1/ai/multi
```

请求：

```json
{"query":"成都3天美食行程","top_k":5}
```

响应：

```json
{"raw_json":"{\"answer\":\"...\"}"}
```

## 配置

```text
NACOS_ADDR=nacos-service
AI_ENGINE_URL=http://ai-service:8000
```

JWT 复用 user 服务签发的 token；无效/缺失 token 返回 401。

## 边界

- 只做鉴权和代理，不实现 RAG；
- 不缓存；
- 没做 AI 服务内部 mTLS；
- 没有限流。

P5-C2 的一部分：先把 AI 从公网接口变成需要登录的 Kratos 网关。
