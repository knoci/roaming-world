# Roaming World Kubernetes 部署配置

> 这是一套开发/演示用的 K8s 编排：8 个 Kratos 业务服务 + AI Engine + MCP + PostgreSQL + MySQL（仅 Nacos 使用）+ Redis + Nacos
> + Kafka + Meilisearch + etcd。生产环境请把数据库/中间件换成托管服务或 Operator，不要直接照搬。

## 单机 minikube

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\k8s-up.ps1
```

使用 [k8s-single-node](../k8s-single-node/kustomization.yaml) overlay：

- 所有业务和基础设施 1 副本；
- MySQL 0 副本，Nacos 用 embedded storage；
- 业务镜像 `IfNotPresent`，由脚本构建并 `minikube image load`；
- 单节点 Kafka 的 controller quorum 用 `localhost:9093`，避免 ClusterIP hairpin；
- PostgreSQL 迁移 Job + Nacos 配置导入 Job。

这是单节点验证，不是生产高可用。

## 1. 包含什么

### 业务服务

| Deployment | Service | 端口 |
|---|---|---|
| user-service | user-service | 8000 HTTP / 9000 gRPC |
| article-service | article-service | 8000 / 9000 |
| food-service | food-service | 8000 / 9000 |
| scene-service | scene-service | 8000 / 9000 |
| comment-service | comment-service | 8000 / 9000 |
| audiobook-service | audiobook-service | 8000 / 9000 |
| plan-service | plan-service | 8000 / 9000 |
| ai-service | ai-service | 8000 |
| mcp-service | mcp-service | 8000 |
| gateway-service | gateway-service | 8000 / 9000 |

每个 Deployment：

- 2 副本；
- RollingUpdate：maxSurge=1，maxUnavailable=0；
- liveness/readiness 探针：GET /healthz:8000；
- requests：128Mi / 100m，limits：256Mi / 200m；
- 挂载 ConfigMap roaming-world-service-config 到 /app/configs/config.yaml。

### 基础设施

| 组件 | 用途 | 说明 |
|---|---|---|
| PostgreSQL | 业务数据库 | postgres-service:5432，PVC 10Gi |
| MySQL | Nacos 配置存储 | 仅 Nacos 使用，不是业务库 |
| Redis | 缓存/排行榜 | redis-service:6379，PVC 5Gi |
| Nacos | 注册/配置中心 | nacos-service:8848，NodePort 30848 |
| Kafka | 评论计数事件 comment-events（不接日志） | kafka-service:9092，单节点 KRaft |
| Meilisearch | 场景搜索 | meilisearch-service:7700 |
| etcd | 验证码 | etcd-service:2379 |

## 2. 文件说明

| 文件 | 作用 |
|---|---|
| namespace.yaml | 三个 Namespace：roaming-world / dev / test |
| secrets.yaml | MySQL/PostgreSQL/Meili/JWT 密钥（示例值） |
| configmaps.yaml | Nacos/MySQL/PostgreSQL 配置 + 服务启动 config.yaml |
| postgres.yaml | 业务 PostgreSQL |
| postgres-migration-job.yaml | 一次性 schema 迁移 Job |
| postgres-migrations/ | K8s 侧 SQL 副本（与 deploy/postgres 保持同步） |
| infrastructure.yaml | MySQL（Nacos）+ Redis |
| nacos.yaml | Nacos + NodePort |
| messaging.yaml | Kafka + Meilisearch |
| etcd.yaml | etcd |
| deployments.yaml | user/article/food/scene |
| deployments-2.yaml | comment/audiobook |
| plan.yaml | plan-service |
| ai.yaml | ai-service（Python RAG） |
| mcp.yaml | mcp-service |
| gateway.yaml | gateway-service |
| service.yaml | 6 个 ClusterIP Service（plan 在 plan.yaml） |
| ingress.yaml | 可选七层入口，按 /v1/* 前缀路由，需要 ingress-nginx |
| kustomization.yaml | 统一编排、镜像 tag、副本数、配置挂载 patch |
| roaming-world.yaml.example | 导入 Nacos 的业务配置示例 |

## 3. 部署步骤

### 3.1 前置条件

- 一个可用的 K8s 集群；
- kubectl 已配置；
- 集群有默认 StorageClass（PVC 能绑定）；
- 镜像已推送到仓库，或本地 kind/minikube 能加载。

### 3.2 渲染/检查

```bash
# 只渲染，不连集群
kubectl kustomize k8s > /tmp/roaming.yaml

# 有集群时
kubectl apply -k k8s
```

### 3.2.1 初始化 PostgreSQL

本地 Compose 会自动执行 `deploy/postgres/migrations/001_init.sql`；K8s 由 `postgres-migrate` Job 执行 `k8s/postgres-migrations/001_init.sql`，服务里的 AutoMigrate 只作为兜底。

迁移 SQL 改动后，Job 的 ConfigMap hash 会变化，而 Job template 不可变；需要先 `kubectl delete job postgres-migrate -n roaming-world` 再重新 `kubectl apply -k k8s`。

### 3.3 初始化 Nacos 数据库

MySQL 只给 Nacos 用，首次启动前要确保 `nacos_config` 库存在并导入 Nacos 的 schema；
如果使用云数据库，直接在控制台初始化即可。业务库是 PostgreSQL，不要混用。

### 3.4 导入 Nacos 配置

业务服务启动时会从 Nacos 拉 roaming-world.yaml，所以要先在 Nacos 建配置：

1. 访问 Nacos 控制台：
   - kubectl get nodes -o wide
   - http://<NODE_IP>:30848/nacos
   - 演示清单关闭了 Nacos 鉴权；生产要开启并配置独立 token
2. 新建配置：
   - Data ID：roaming-world.yaml
   - Group：DEFAULT_GROUP
   - 内容：参考 k8s/roaming-world.yaml.example，把密码/COS/邮箱替换成真实值。
3. 如果服务已经因为拉不到配置 CrashLoop，导入后重启：

```bash
kubectl rollout restart deployment -n roaming-world
```

### 3.5 检查

```bash
kubectl get pods -n roaming-world
kubectl get svc -n roaming-world
kubectl get endpoints -n roaming-world
kubectl logs -n roaming-world deployment/user-service
```

## 4. 服务配置怎么注入

没有让代码读一堆环境变量，而是用 ConfigMap 挂文件：

```yaml
volumeMounts:
- name: service-config
  mountPath: /app/configs/config.yaml
  subPath: config.yaml
  readOnly: true
volumes:
- name: service-config
  configMap:
    name: roaming-world-service-config
```

roaming-world-service-config 里只有：

- HTTP/gRPC 监听地址；
- Nacos 地址 nacos-service:8848；
- Data ID / Group。

业务数据库、Redis、Kafka、JWT 等仍在 Nacos 的 roaming-world.yaml 里，符合“配置中心统一管理”的设计。

## 5. 请求怎么走

### 集群内 HTTP

```
Client -> Service ClusterIP -> kube-proxy -> 某个 Pod:8000
```

### gRPC

```
comment -> Nacos discovery(discovery:///user.grpc) -> Kratos selector(wrr) -> user Pod:9000
```

K8s Service 是四层负载均衡；Nacos 是应用层服务发现。两者不是重复。

## 6. 常用运维命令

```bash
# 滚动更新
kubectl set image deployment/user-service user-service=roaming-world/user:v2.0.0 -n roaming-world
kubectl rollout status deployment/user-service -n roaming-world
kubectl rollout undo deployment/user-service -n roaming-world

# 扩容
kubectl scale deployment/user-service -n roaming-world --replicas=3

# 排查
kubectl describe pod <pod> -n roaming-world
kubectl logs <pod> -n roaming-world --previous
kubectl get endpoints user-service -n roaming-world
```

## 7. 探针和资源

- readiness 失败：Pod 从 EndpointSlice 移除，不再接流量；
- liveness 失败：容器重启；
- 当前没有 startupProbe，启动慢可以加；
- requests/limits 是演示值，生产按 P95 压测调整；
- 所有业务服务是 Burstable QoS。

## 8. 部署边界

已做：

- Deployment/Service/ConfigMap/Secret/Kustomize；
- 多副本 + 滚动更新；
- readiness/liveness；
- requests/limits；
- PostgreSQL/Redis/Nacos/Kafka/Meili/etcd 单节点编排；
- PostgreSQL schema 迁移 Job；
- ConfigMap 挂载 Nacos 启动配置。

没做：

- HPA（没有 metrics-server/HPA）；
- 生产级网关/Ingress Controller 未安装；ingress.yaml 只是可选清单，业务 Service 仍只有 ClusterIP；
- NetworkPolicy/RBAC/非 root/PodSecurity；
- Prometheus/Grafana/ELK；
- MySQL/Redis/Nacos/Kafka 的高可用集群；
- 生产级备份/恢复；
- 多环境 Kustomize overlay（目前只有 ConfigMap/Namespace）。

## 9. 常见故障

### Pod Pending

- 资源不够；
- PVC 没绑定/没有 StorageClass；
- 节点污点/亲和性。

### ImagePullBackOff

- 镜像名/tag 错；
- 私有仓库没配 imagePullSecret。

### CrashLoopBackOff

- 配置缺失（Nacos/COS/etcd）；
- 依赖没起来；
- 看 kubectl logs --previous。

### Service 503 / 没有 Endpoints

- selector 和 Pod label 不匹配；
- readiness 没通过；
- 端口名/目标端口错；
- NetworkPolicy 拦截。

### Nacos 配置拉不到

- Data ID/Group/Namespace 不一致；
- Nacos 没就绪；
- 服务挂载的 config.yaml 里地址不是 nacos-service。

## 10. 清理

```bash
kubectl delete -k k8s
```
