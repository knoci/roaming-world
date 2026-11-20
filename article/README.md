# Article Service

文章服务，负责文章发布审核、点赞、收藏、视频文章和排序。

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
- `internal/data`：PostgreSQL 持久化和 Kafka 评论计数消费；
- `cmd/article`：Wire 组装、Nacos 注册。


## 内容生命周期

```text
draft    -> pending -> published -> offline
rejected -> pending -> rejected
```

- 创建文章默认 draft；
- 作者调 submit 进入 pending；
- admin 调 review：approve / reject / offline；
- 编辑、提交和审核都会更新 `revision`。审核界面可把读取时的版本通过 `expected_revision` 提交，文章已变化时收到 HTTP 409，需刷新后重新判断；
- 公开列表和详情只展示 published；
- 作者用 `GET /v1/article?mine=true` 查看自己的草稿/驳回；
- 点赞/收藏只允许 published。

## 排序与分页

列表从 PostgreSQL 查询当前发布状态和计数。支持 `way=time|likes|comments|favorites`，`reverse=true` 为降序，同分时按文章 ID 排序。

评论事件通知文章服务重新统计目标文章的有效评论与回复。处理或提交 Kafka 偏移失败时，消费者会重试当前步骤。同一事件重复或乱序到达时，计数仍取 PostgreSQL 中的当前状态。

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
