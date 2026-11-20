# User Service

用户服务：注册、登录、JWT、资料、头像、关注关系、通知。

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | /v1/user/register | 注册 |
| GET | /v1/user/verify | 向邮箱发送指定用途的验证码 |
| POST | /v1/user/email/confirm | 预校验验证码 |
| POST | /v1/user/login | 登录 |
| POST | /v1/user/reset | 重置密码 |
| GET | /v1/user/find | 查找用户 |
| PUT | /v1/user/info | 更新资料 |
| POST | /v1/user/avatar | 上传头像 |
| POST | /v1/user/{uid}/follow | 关注 |
| DELETE | /v1/user/{uid}/follow | 取消关注 |
| GET | /v1/user/{uid}/followers | 粉丝列表 |
| GET | /v1/user/{uid}/following | 关注列表 |
| GET | /v1/user/notifications | 我的通知 |
| POST | /v1/user/notifications/{id}/read | 标记已读 |

## 邮箱验证码

- 发码请求传入 `email` 和 `purpose`。用途为 `register`（默认）、`reset_password` 或 `change_email`。更换邮箱时须使用当前账号的 Bearer Token，验证码绑定该账号与目标邮箱。
- 发码成功返回空响应对象，验证码通过邮箱交付。同一邮箱每 60 秒最多发码一次，每个验证码有效期 10 分钟，累计输错 5 次后失效。
- 如需调用预校验，提交与发码一致的用途。预校验成功后，最终的注册、密码重置或资料更新仍须提交验证码并完成一次性消费。
- 升级后，现有客户端需要从邮件取码。升级前签发的验证码需要重新申请。

## 角色

users 表有：

```text
role:   user / admin
status: active / banned
```

管理接口用 JWT 里的 role=admin，不再使用固定密码。

## 社交规则

- 不能关注自己；
- 不能关注不存在或 banned 的用户；
- 关注幂等：重复关注不会重复插入；
- 首次关注会生成一条 `follow` 通知；
- 关注/粉丝列表分页，limit 最大 50；
- 通知只能标记自己的。

表：

- follows(follower_uid, followee_uid)
- notifications(uid, type, actor_uid, payload, read_at)

通知类型：follow、comment_reply。comment 服务在回复时调内部 CreateNotification。
