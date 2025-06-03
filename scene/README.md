# Scene 服务

## 项目介绍

Scene 服务是 Roaming World 平台的核心组件之一，负责管理和提供场景（Scene）相关的功能。该服务基于 [Kratos](https://github.com/go-kratos/kratos) 微服务框架构建，提供了场景的创建、查询、更新、删除、搜索等功能，支持高性能的全文搜索和多级缓存策略。

## 功能特点

- **场景管理**：支持场景的创建、更新、删除和查询操作
- **高性能搜索**：集成 MeiliSearch 提供毫秒级的全文搜索能力
- **多级缓存**：采用 Redis 缓存减轻数据库负载，提高响应速度
- **RESTful API**：提供标准的 HTTP/gRPC 接口
- **数据持久化**：使用 PostgreSQL 数据库存储场景数据
- **日志追踪**：集成 Kafka 进行 SQL 和错误日志收集
- **优雅降级**：搜索和查询支持多级回退策略

## 架构设计

### 分层架构

Scene 服务采用 Kratos 推荐的分层架构：

- **API 层**：定义服务接口和数据结构（Protocol Buffers）
- **Service 层**：实现 API 接口，处理请求和响应
- **Biz 层**：实现业务逻辑，定义领域模型和仓库接口
- **Data 层**：实现数据访问逻辑，包括数据库、缓存和搜索引擎操作

### 技术栈

- **框架**：Kratos v2
- **数据库**：PostgreSQL
- **缓存**：Redis
- **搜索引擎**：MeiliSearch
- **消息队列**：Kafka（用于日志收集）
- **API 协议**：HTTP/gRPC
- **配置管理**：Nacos

## 性能优化

### 多级搜索策略

Scene 服务实现了高效的多级搜索策略：

1. **MeiliSearch 优先**：首先使用 MeiliSearch 进行全文搜索，直接从搜索结果构建场景对象
2. **Redis 补充**：如果 MeiliSearch 中缺少某些字段，从 Redis 缓存补充
3. **数据库回退**：当 MeiliSearch 和 Redis 都无法提供完整数据时，从数据库获取

### 缓存优化

- **场景数据缓存**：将完整的场景数据缓存到 Redis，减少数据库访问
- **场景列表缓存**：缓存场景 ID 列表和总数，优化列表查询性能
- **异步缓存更新**：使用 goroutine 异步更新缓存，不阻塞主请求流程

### 搜索优化

- **直接使用搜索结果**：从 MeiliSearch 结果直接构建场景对象，减少额外查询
- **智能字段补充**：只在必要时从 Redis 或数据库补充缺失字段
- **异步索引更新**：后台异步更新搜索索引，确保数据一致性

## 安装部署

### 环境要求

- Go 1.16+
- PostgreSQL
- Redis
- MeiliSearch
- Kafka

### 安装步骤

1. 克隆代码库

```bash
git clone https://github.com/knoci/roaming-world/scene.git
cd scene
```

2. 安装依赖

```bash
go mod tidy
```

3. 配置服务

编辑 `configs/config.yaml` 文件，配置数据库、Redis、MeiliSearch 和 Kafka 连接信息。

4. 编译运行

```bash
make build
./bin/scene -conf ./configs
```

### Docker 部署

```bash
# 构建镜像
docker build -t roaming-world/scene .

# 运行容器
docker run --rm -p 8000:8000 -p 9000:9000 -v /path/to/configs:/data/conf roaming-world/scene
```

## API 接口

Scene 服务提供以下 HTTP API 接口：

- `POST /v1/scene` - 创建场景
- `DELETE /v1/scene/{sid}` - 删除场景
- `PUT /v1/scene` - 更新场景
- `GET /v1/scene/search` - 搜索场景
- `GET /v1/scene` - 获取场景列表
- `GET /v1/scene/{sid}` - 根据 ID 获取场景

详细的 API 文档请参考 `openapi.yaml` 文件。

## 开发指南

### 生成代码

```bash
# 生成 API 代码
make api

# 生成所有代码
make all
```

### 依赖注入

使用 Wire 进行依赖注入：

```bash
cd cmd/scene
wire
```

## 贡献指南

1. Fork 代码库
2. 创建功能分支 (`git checkout -b feature/amazing-feature`)
3. 提交更改 (`git commit -m 'Add some amazing feature'`)
4. 推送到分支 (`git push origin feature/amazing-feature`)
5. 创建 Pull Request

## 许可证

本项目采用 MIT 许可证 - 详情请参阅 [LICENSE](LICENSE) 文件。

