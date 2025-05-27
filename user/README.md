# Roaming World - 用户服务

## 项目介绍

Roaming World 用户服务是基于 Kratos 微服务框架开发的用户管理系统，提供用户注册、登录、信息管理等功能。本服务采用了现代化的微服务架构，集成了 Nacos 配置中心、Kafka 消息队列、PostgreSQL 数据库和 etcd 分布式键值存储等组件。

## 技术栈

- **框架**: [Kratos](https://github.com/go-kratos/kratos) v2
- **数据库**: PostgreSQL
- **缓存/配置存储**: etcd
- **配置中心**: Nacos
- **消息队列**: Kafka
- **对象存储**: 腾讯云 COS
- **API**: gRPC + HTTP (RESTful)
- **依赖注入**: Wire
- **日志**: Kratos 内置日志

## 系统架构

```
+------------------+        +------------------+        +------------------+
|                  |        |                  |        |                  |
|  HTTP/gRPC API   |<------>|  业务逻辑层(biz) |<------>|   数据访问层     |
|                  |        |                  |        |                  |
+------------------+        +------------------+        +------------------+
                                                               |
                                                               |
                                                               v
                              +------------------+    +------------------+
                              |                  |    |                  |
                              |   Nacos 配置中心  |    |  PostgreSQL 数据库|
                              |                  |    |                  |
                              +------------------+    +------------------+
                                       |
                                       |
                              +------------------+    +------------------+
                              |                  |    |                  |
                              |  Kafka 消息队列   |    |   etcd 键值存储   |
                              |                  |    |                  |
                              +------------------+    +------------------+
```

## 功能特性

- **用户管理**:
  - 用户注册与邮箱验证
  - 用户登录与JWT认证
  - 用户信息查询与更新
  - 用户头像上传与管理
  - 用户密码重置

- **高性能设计**:
  - 使用Kafka消息队列处理异步任务
  - 批量处理提高吞吐量
  - 并行消费者模式

- **可靠性保障**:
  - 分布式配置管理
  - 服务健康检查
  - 日志记录与监控

## 安装与部署

### 前置条件

- Go 1.19+
- PostgreSQL
- Nacos
- Kafka
- etcd

### 本地开发环境搭建

1. **安装 Kratos 命令行工具**

```bash
go install github.com/go-kratos/kratos/cmd/kratos/v2@latest
```

2. **安装依赖**

```bash
make init
```

3. **配置本地环境**

编辑 `configs/config.yaml` 文件，配置数据库、Nacos等连接信息。

4. **生成API代码**

```bash
make api
```

5. **编译项目**

```bash
make build
```

6. **运行服务**

```bash
./bin/user -conf ./configs
```

### Docker部署

```bash
# 构建Docker镜像
docker build -t roaming-world/user-service .

# 运行容器
docker run --rm -p 8000:8000 -p 9000:9000 -v /path/to/your/configs:/data/conf roaming-world/user-service
```

## API文档

服务提供了HTTP和gRPC两种访问方式：

- HTTP服务端口: 8000
- gRPC服务端口: 9000

### 主要API接口

- **用户注册**: `POST /v1/user/register`
- **发送验证码**: `GET /v1/user/verify`
- **用户登录**: `POST /v1/user/login`
- **查找用户**: `GET /v1/user/find`
- **删除用户**: `DELETE /v1/user`
- **更新用户信息**: `PUT /v1/user`
- **重置密码**: `POST /v1/user/reset-password`
- **上传头像**: `POST /v1/user/avatar`

详细API文档可通过生成的OpenAPI规范查看。

## 项目结构

```
├── api                 # API定义
│   └── user            # 用户服务API
│       └── v1          # API版本
├── cmd                 # 入口文件
│   └── user            # 用户服务入口
├── configs             # 配置文件
├── internal            # 内部代码
│   ├── biz             # 业务逻辑层
│   ├── conf            # 配置结构
│   │   └── nacos       # Nacos配置
│   ├── data            # 数据访问层
│   ├── pkg             # 内部公共包
│   ├── server          # 服务器实现
│   └── service         # 服务实现
└── third_party         # 第三方依赖
```

## 开发指南

### 添加新API

1. 在 `api/user/v1/user.proto` 中定义新的服务方法
2. 执行 `make api` 生成代码
3. 在 `internal/service` 中实现服务方法
4. 在 `internal/biz` 中添加业务逻辑
5. 在 `internal/data` 中实现数据访问

### 使用Kafka消息队列

项目中已集成Kafka消息队列，可用于处理异步任务：

```go
// 发送消息
message := pkg.NewMessage("key", []byte("value"))
kafkaSender.Send(ctx, message)

// 接收消息
kafkaReceiver.Receive(ctx, func(ctx context.Context, msg *pkg.Message) error {
    // 处理消息
    return nil
})

// 并行接收处理
kafkaReceiver.ReceiveParallel(ctx, handler, 10) // 10个并行worker
```

## 贡献指南

1. Fork 本仓库
2. 创建您的特性分支 (`git checkout -b feature/amazing-feature`)
3. 提交您的更改 (`git commit -m 'Add some amazing feature'`)
4. 推送到分支 (`git push origin feature/amazing-feature`)
5. 打开一个 Pull Request

## 许可证

本项目采用 MIT 许可证 - 详见 [LICENSE](LICENSE) 文件

