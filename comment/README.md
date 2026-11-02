# Comment Service

评论服务：评论、两级回复、点赞、分页、软删除、评论计数 outbox。

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | /v1/comment | 创建评论/回复 |
| DELETE | /v1/comment/{cid} | 软删除自己的评论 |
| POST | /v1/comment/{cid}/like | 点赞（幂等） |
| DELETE | /v1/comment/{cid}/like | 取消点赞（幂等） |
| GET | /v1/comment?aid=&page=&limit= | 一级评论分页 |
| GET | /v1/comment/with-replies?aid=&page=&limit= | 一级评论 + 每条前 2 条回复 |
| GET | /v1/comment/{cid}/replies?page=&limit= | 回复分页 |
| POST | /v1/comment/{cid}/report | 举报评论 |
| GET | /v1/comment/reports?status=pending | admin 举报队列 |
| POST | /v1/comment/reports/{id}/resolve | admin 处理举报 |

请求头：`Authorization: Bearer <token>`（列表可匿名）。

## 规则

- 只保留两级：回复二级评论时自动折叠到根评论；
- 父评论必须 active 且属于同一 target；
- 评论列表默认 page=1、limit=20，最大 50；
- 删除是软删除，内容替换为“该评论已删除”；
- 删除根评论会软删除其回复；
- 评论计数只统计 active 评论；
- 计数通过 comment_outbox 发 Kafka，article 消费更新 articles.comments。
- 举报去重：同一用户对同一评论只能举报一次；admin 可 remove（软删除）或 dismiss。
- 回复时调用 user.CreateNotification，给被回复用户生成 comment_reply 通知；通知失败不影响评论写入。

## 分层

```text
api/comment/v1/comment.proto
internal/service -> internal/biz -> internal/data
```
