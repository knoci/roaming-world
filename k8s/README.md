# Roaming World Kubernetes 部署配置

本目录包含了基于 Kratos 框架的 Roaming World 微服务项目的完整 Kubernetes 部署配置。

## 项目架构

### 微服务列表
- **user-service**: 用户服务 (HTTP: 8000, gRPC: 9000)
- **article-service**: 文章服务 (HTTP: 8000, gRPC: 9000)
- **food-service**: 美食服务 (HTTP: 8000, gRPC: 9000)
- **scene-service**: 场景服务 (HTTP: 8000, gRPC: 9000)
- **comment-service**: 评论服务 (HTTP: 8000, gRPC: 9000)
- **audiobook-service**: 有声书服务 (HTTP: 8000, gRPC: 9000)

### 基础设施服务
- **MySQL**: 数据库服务 (端口: 3306)
- **Redis**: 缓存服务 (端口: 6379)
- **Nacos**: 配置中心 (HTTP: 8848, gRPC: 9848/9849)

## 文件说明

| 文件名 | 描述 |
|--------|------|
| `namesapce.yaml` | 命名空间配置 (生产、开发、测试环境) |
| `secrets.yaml` | 敏感信息配置 (数据库密码、JWT密钥等) |
| `configmaps.yaml` | 应用配置信息 |
| `deployments.yaml` | 主要微服务部署配置 |
| `deployments-2.yaml` | 其他微服务部署配置 |
| `service.yaml` | 所有微服务的 Service 配置 |
| `infrastructure.yaml` | MySQL 和 Redis 部署配置 |
| `nacos.yaml` | Nacos 配置中心部署配置 |
| `kustomization.yaml` | Kustomize 统一管理配置 |

## 部署步骤

### 1. 准备工作

确保你的 Kubernetes 集群已经准备就绪，并且 kubectl 已正确配置。

### 2. 创建命名空间

```bash
kubectl apply -f namesapce.yaml
```

### 3. 部署基础设施

```bash
# 部署配置和密钥
kubectl apply -f secrets.yaml
kubectl apply -f configmaps.yaml

# 部署基础设施服务
kubectl apply -f infrastructure.yaml
kubectl apply -f nacos.yaml
```

### 4. 等待基础设施就绪

```bash
# 检查 MySQL 状态
kubectl get pods -n roaming-world -l app=mysql

# 检查 Redis 状态
kubectl get pods -n roaming-world -l app=redis

# 检查 Nacos 状态
kubectl get pods -n roaming-world -l app=nacos
```

### 5. 部署微服务

```bash
# 部署微服务
kubectl apply -f deployments.yaml
kubectl apply -f deployments-2.yaml
kubectl apply -f service.yaml
```

### 6. 使用 Kustomize 一键部署 (推荐)

```bash
# 使用 kustomize 一键部署所有资源
kubectl apply -k .
```

## 访问服务

### Nacos 控制台

Nacos 通过 NodePort 暴露，可以通过以下方式访问：

```bash
# 获取节点 IP
kubectl get nodes -o wide

# 访问 Nacos 控制台
# http://<NODE_IP>:30848/nacos
# 默认用户名/密码: nacos/nacos
```

### 微服务健康检查

```bash
# 检查所有服务状态
kubectl get pods -n roaming-world

# 查看特定服务日志
kubectl logs -n roaming-world deployment/user-service
```

## 配置说明

### 环境变量

所有微服务都通过 ConfigMap 和 Secret 获取配置：

- **数据库配置**: 通过 ConfigMap 获取主机、端口等信息
- **敏感信息**: 通过 Secret 获取密码、密钥等
- **服务发现**: 通过 Nacos 进行服务注册和发现

### 资源限制

每个服务都配置了合理的资源请求和限制：

- **微服务**: 128Mi-256Mi 内存, 100m-200m CPU
- **MySQL**: 512Mi-1Gi 内存, 250m-500m CPU
- **Redis**: 256Mi-512Mi 内存, 100m-200m CPU
- **Nacos**: 512Mi-1Gi 内存, 250m-500m CPU

### 持久化存储

以下服务使用 PVC 进行数据持久化：

- **MySQL**: 10Gi 存储
- **Redis**: 5Gi 存储
- **Nacos**: 5Gi 存储

## 扩容和更新

### 扩容服务

```bash
# 扩容用户服务到 3 个副本
kubectl scale deployment user-service -n roaming-world --replicas=3
```

### 更新镜像

```bash
# 更新用户服务镜像
kubectl set image deployment/user-service user-service=roaming-world/user:v2.0.0 -n roaming-world
```

### 使用 Kustomize 管理

修改 `kustomization.yaml` 中的镜像标签或副本数，然后重新应用：

```bash
kubectl apply -k .
```

## 故障排查

### 查看日志

```bash
# 查看 Pod 日志
kubectl logs -n roaming-world <pod-name>

# 实时查看日志
kubectl logs -n roaming-world <pod-name> -f
```

### 检查服务状态

```bash
# 查看所有资源状态
kubectl get all -n roaming-world

# 查看 Pod 详细信息
kubectl describe pod -n roaming-world <pod-name>
```

### 网络连接测试

```bash
# 进入 Pod 进行网络测试
kubectl exec -it -n roaming-world <pod-name> -- /bin/sh

# 测试数据库连接
kubectl exec -it -n roaming-world <pod-name> -- nc -zv mysql-service 3306
```

## 注意事项

1. **镜像准备**: 确保所有微服务的 Docker 镜像已构建并推送到镜像仓库
2. **存储类**: 根据你的 K8s 集群配置，可能需要指定 StorageClass
3. **网络策略**: 根据安全需求，可能需要配置网络策略
4. **监控告警**: 建议配置 Prometheus + Grafana 进行监控
5. **日志收集**: 建议配置 ELK 或 EFK 进行日志收集

## 清理资源

```bash
# 删除所有资源
kubectl delete -k .

# 或者逐个删除
kubectl delete namespace roaming-world roaming-world-dev roaming-world-test
```