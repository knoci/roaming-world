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

## 技术架构

- **框架**：基于[Kratos](https://github.com/go-kratos/kratos)微服务框架
- **API协议**：支持HTTP和gRPC
- **数据存储**：PostgreSQL数据库
- **缓存**：Redis
- **消息队列**：Kafka
- **配置中心**：Nacos
- **依赖注入**：使用Wire进行依赖注入
- **容器化**：支持Docker部署

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

## 安装与部署

### 前置条件

- Go 1.16+
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

## API接口

### HTTP接口

- `POST /v1/food` - 创建食物
- `GET /v1/food` - 获取食物列表
- `GET /v1/food/random` - 获取随机食物

### gRPC接口

- `CreateFood` - 创建食物
- `GetFoodList` - 获取食物列表
- `GetRandomFood` - 获取随机食物

## 性能优化

- 使用Redis缓存食物数据，提高查询性能
- 在`CreateFood`方法中将新创建的食物信息同时保存到Redis
- `GetFoodList`和`GetRandomFood`方法优先从Redis获取数据，提高响应速度
- 使用24小时的缓存过期时间，平衡数据一致性和性能

## 开发指南

### 生成API代码

```bash
make api
```

### 生成配置代码

```bash
make config
```

### 生成依赖注入代码

```bash
cd cmd/food
wire
```

## 贡献指南

欢迎提交Issue和Pull Request，一起完善项目。

## 许可证

本项目采用MIT许可证。

