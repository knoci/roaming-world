# 食物服务 (Food Service)

## 项目介绍

食物服务是漫游世界(Roaming World)项目的一个微服务组件，负责管理和提供食物相关的数据和功能。该服务基于Kratos框架开发，提供了食物的创建、查询和随机获取等功能，支持HTTP和gRPC协议。

## 功能特点

- **食物管理**：创建和存储食物信息，包括名称、描述、食谱、文章、位置等
- **食物查询**：获取食物列表，支持随机排序
- **随机食物**：随机获取一个食物信息
- **高性能缓存**：使用Redis缓存食物数据，提高查询性能
- **消息队列**：使用Kafka进行数据同步和事件通知
- **配置中心**：支持Nacos配置中心，实现配置的动态管理
- **认证授权**：API接口支持基于Token的认证

## 技术架构

- **框架**：基于[Kratos](https://github.com/go-kratos/kratos)微服务框架
- **API协议**：支持HTTP和gRPC
- **数据存储**：PostgreSQL数据库
- **缓存**：Redis
- **消息队列**：Kafka
- **配置中心**：Nacos
- **依赖注入**：使用Wire进行依赖注入
- **容器化**：支持Docker部署
- **API验证**：使用protoc-gen-validate进行请求参数验证

## 项目结构

```
├── api                  # API定义目录
│   └── food            # 食物服务API
│       └── v1          # API版本
├── cmd                 # 应用入口
│   └── food           # 食物服务入口
├── configs             # 配置文件
├── internal            # 内部代码
│   ├── biz            # 业务逻辑层
│   ├── conf           # 配置结构
│   ├── data           # 数据访问层
│   ├── pkg            # 内部公共包
│   ├── server         # 服务实现
│   └── service        # 服务接口实现
├── third_party         # 第三方依赖
```

### 分层架构

- **Service层**：处理API请求，参数验证，调用业务逻辑
- **Biz层**：实现核心业务逻辑，定义领域模型和仓库接口
- **Data层**：实现数据访问，包括数据库操作、缓存和消息队列

## API接口

服务提供以下API接口：

### 创建食物

```
POST /v1/food
```

请求参数：
- `name`: 食物名称 (必填，1-50字符)
- `view`: 食物视图 (必填，1-10项)
- `describe`: 食物描述 (必填)
- `recipe`: 食谱 (必填)
- `article`: 文章 (必填)
- `location`: 位置 (必填，1-30字符)

需要在请求头中包含`Authorization: knoci1337`进行认证。

### 获取食物列表

```
GET /v1/food
```

返回所有食物信息，结果随机排序。

### 获取随机食物

```
GET /v1/food/random
```

随机返回一个食物信息。

## 安装与部署

### 前置条件

- Go 1.21+
- PostgreSQL
- Redis
- Kafka
- Nacos (可选，用于配置中心)

### 本地开发

1. 克隆项目

```bash
git clone https://github.com/knoci/roaming-world.git
cd roaming-world/food
```

2. 安装依赖

```bash
make init
```

3. 生成代码

```bash
make all
```

4. 配置数据库和Redis

修改`configs/config.yaml`文件，配置Nacos地址，或者直接在Nacos中配置相关参数。

5. 编译运行

```bash
make build
./bin/food -conf ./configs
```

### Docker部署

1. 构建Docker镜像

```bash
docker build -t roaming-world/food .
```

2. 运行容器

```bash
docker run --rm -p 8000:8000 -p 9000:9000 -v /path/to/your/configs:/data/conf roaming-world/food
```

## 配置说明

服务配置支持两种方式：

1. 本地配置文件：`configs/config.yaml`
2. Nacos配置中心：通过`configs/config.yaml`中的Nacos配置连接到配置中心

主要配置项：

- 服务地址和端口
- 数据库连接信息
- Redis连接信息
- Kafka连接信息

## 开发指南

### 添加新API

1. 在`api/food/v1/food.proto`中定义新的服务方法和消息
2. 执行`make api`生成API代码
3. 在`internal/biz`中实现业务逻辑
4. 在`internal/data`中实现数据访问
5. 在`internal/service`中实现服务接口

### 依赖注入

项目使用[Wire](https://github.com/google/wire)进行依赖注入，修改依赖关系后需要执行：

```bash
go generate ./...
```

## 贡献指南

1. Fork 项目
2. 创建功能分支 (`git checkout -b feature/amazing-feature`)
3. 提交更改 (`git commit -m 'Add some amazing feature'`)
4. 推送到分支 (`git push origin feature/amazing-feature`)
5. 创建Pull Request

## 许可证

本项目采用 MIT 许可证 - 详情请参阅 [LICENSE](LICENSE) 文件。

