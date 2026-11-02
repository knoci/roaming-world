# Article Service

文章服务，负责文章 CRUD、点赞、收藏、视频文章和排行榜缓存。

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | /v1/article | 创建文章 |
| PUT | /v1/article | 更新文章 |
| DELETE | /v1/article/{aid} | 删除文章 |
| POST | /v1/article/{aid}/submit | 作者提交审核 |
| POST | /v1/article/{aid}/review | 管理员审核/下架 |
| POST | /v1/article/{aid}/like | 点赞（幂等） |
| DELETE | /v1/article/{aid}/like | 取消点赞（幂等） |
| POST | /v1/article/{aid}/favorite | 收藏（幂等） |
| DELETE | /v1/article/{aid}/favorite | 取消收藏（幂等） |
| GET | /v1/article/feed | 关注作者的已发布文章 Feed |
| GET | /v1/article | 文章列表；默认只返回 published，`mine=true` 看自己的，`status=` 仅 admin |
| GET | /v1/article/{aid} | 文章详情 |
| GET | /v1/article/like/list | 用户点赞列表 |
| GET | /v1/article/favorite/list | 用户收藏列表 |
| GET | /v1/article/video/{videoid} | 根据视频 ID 获取文章 |
| GET | /v1/article/video/count | 最大视频 ID |

请求头需要 `Authorization: Bearer <token>`（除列表、详情、视频查询外）。

## 分层

- `api/article/v1/article.proto`：HTTP + gRPC 契约；
- `internal/service`：JWT claims、pb 转换；
- `internal/biz`：Article 实体、ArticleRepo 接口、用例；
- `internal/data`：GORM、Redis zset、Kafka CDC；
- `cmd/article`：Wire 组装、Nacos 注册。


## 内容生命周期

```text
draft -> pending -> published -> offline
                 -> rejected -> draft
```

- 创建文章默认 draft；
- 作者调 submit 进入 pending；
- admin 调 review：approve / reject / offline；
- 公开列表和详情只展示 published；
- 作者用 `GET /v1/article?mine=true` 查看自己的草稿/驳回；
- 点赞/收藏只允许 published。

## 缓存

Redis 使用四个 zset：

- `article:time`
- `article:likes`
- `article:comments`
- `article:favorites`

列表优先读 zset，未命中回源 PostgreSQL 并重建。
点赞/收藏在事务提交后用 `ZIncrBy` 更新分数。

## 生成代码

```bash
cd article
protoc --proto_path=./api --proto_path=./third_party \
  --go_out=paths=source_relative:./api \
  --go-grpc_out=paths=source_relative:./api \
  --go-http_out=paths=source_relative:./api \
  --validate_out=lang=go,paths=source_relative:./api \
  --go-errors_out=paths=source_relative:./api \
  api/article/v1/article.proto api/article/v1/error_reason.proto
```

## 启动

```bash
go run ./cmd/article -conf ./configs
```
