# Plan Service

行程聚合服务：一个用户的一次旅行，包含按天组织的行程项。

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | /v1/plan | 创建行程，自动按日期生成 day |
| PUT | /v1/plan/{id} | 修改标题/城市/预算/可见性 |
| GET | /v1/plan/{id} | 行程详情 |
| GET | /v1/plan?scope=mine|public | 我的行程 / 公开行程 |
| POST | /v1/plan/{id}/publish | 发布行程 |
| POST | /v1/plan/{id}/items | 添加行程项 |
| DELETE | /v1/plan/{id}/items/{item_id} | 删除行程项 |
| DELETE | /v1/plan/{id} | 删除行程 |

请求头：`Authorization: Bearer <token>`。公开行程详情可匿名访问。

## 业务规则

- 日期格式 YYYY-MM-DD，结束日期不能早于开始日期；
- 最长 30 天；
- 创建时自动生成 travel_plan_days；
- 只有 draft 行程可编辑/加 item/发布；
- item 类型只允许 scene / food / article；
- 时间格式 HH:MM，且 start < end；
- 同一天 item 时间不能重叠；
- 发布前至少有一个 item；
- 公开列表只返回 published + public。

## 表

- travel_plans
- travel_plan_days
- travel_plan_items

## 分层

```text
api/plan/v1/plan.proto
internal/service -> internal/biz -> internal/data
```

P1 MVP 只做 owner 权限；协作者 travel_plan_members 留到后续。
