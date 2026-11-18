# 观猹（Watcha）OAuth2 登录

> 状态：已实现，2026-11-18。接入参考：[观猹 OAuth2 接入文档](https://watcha.cn/oauth/authorize)（部署时以观猹提供的最新文档为准）。

## 1. 目标与范围

登录页增加「使用观猹账号登录」。使用标准 OAuth2 Authorization Code + PKCE，由后端持有 `client_secret` 并完成授权码交换；浏览器只拿一次性授权码，不接触 client secret，也不把本站 JWT 放进 URL。

- 新用户首次登录时，按当前后台「开放注册」开关决定是否自动创建本站普通用户。
- 已绑定用户在注册关闭后仍可登录。
- 观猹用户 ID 与本站用户通过 `user_oauth_identities(provider='watcha', subject=<user_id>)` 绑定。
- 不保存观猹 access token；登录完成后只保留本站 JWT。
- 不改变 TokenPay 钱包、模型渠道和平台积分路径。

## 2. 回调与流程

回调地址固定为：

```text
https://canvas.tan90.site/auth/watcha/callback
```

1. 登录页调用 `GET /api/v1/auth/watcha/start?next=/目标路径`。
2. 后端生成 PKCE verifier、S256 challenge 和一次性 state，把 state、verifier、next 存入 Redis（无 Redis 时降级进程内存，仅适合单实例），返回观猹授权页地址。
3. 用户在观猹确认授权，浏览器 302 到前端 `/auth/watcha/callback?code=...&state=...`。
4. 前端调用 `POST /api/v1/auth/watcha/callback`，请求体 `{flow, code}`；`flow` 就是上一步的 state。
5. 后端一次性消费 state，向观猹换取 access token，再读取 `userinfo`；按 `user_id` 查绑定用户，必要时在事务内创建用户和身份，最后签发本站 JWT。
6. 前端保存 JWT，并跳转到 start 时记录的站内 `next`。

## 3. 配置

```dotenv
APP_WATCHA_CLIENT_ID=...
APP_WATCHA_CLIENT_SECRET=...
APP_WATCHA_REDIRECT_URI=https://canvas.tan90.site/auth/watcha/callback
```

三项必须同时配置；未配置或只配置一部分时，登录页不显示观猹入口，配置不完整会让后端启动校验失败。`client_secret` 只放部署机的 `.env` 或密钥管理系统，不写进仓库、镜像、日志或前端构建产物。

观猹后台登记的 `domain` 必须允许上面的回调地址，授权请求的 `scope` 当前为 `read`（基础资料）。如果需要邮箱或手机号，需要先在观猹侧开通对应 scope，再修改代码中的 `watchaScope`，并处理用户未绑定字段的情况。

## 4. 安全约定

- OAuth 请求使用 `state` + PKCE S256，state 一次性消费，默认 10 分钟过期。
- token 交换请求包含 `client_secret`，使用不记录请求正文的敏感出站客户端。
- JWT 只在回调页通过 POST 响应返回，不进入地址栏。
- `next` 只接受站内绝对路径，拒绝 `//`、外部 URL 和 `/login` 回跳。
- 第三方登录创建的用户始终是普通用户 `user`，不因为本站用户表为空而获得超级管理员权限；首个站内账号仍通过正常注册流程产生。
- 已停用账号即使观猹授权成功也不能登录。

## 5. 数据与接口

迁移 `0041_watcha_oauth.sql` 创建 `user_oauth_identities`，唯一索引为 `(provider, subject)`。

| 方法 | 路径                             | 说明                                       |
| ---- | -------------------------------- | ------------------------------------------ |
| GET  | `/api/v1/auth/watcha/start`      | 返回 `{auth_url}`，无需登录                |
| POST | `/api/v1/auth/watcha/callback`   | 请求 `{flow, code}`，返回 `{token, expire_at, role, next}`，无需登录 |
| GET  | `/api/v1/auth/config`            | 响应新增 `watcha_enabled`，登录页据此显示入口 |

## 6. 验收

- 配置完整时登录页出现「使用观猹账号登录」。
- 观猹授权成功后能进入本站；新用户会创建本站普通账号并写入身份绑定。
- 刷新/重复回调不能复用同一个 state 或 code。
- 注册关闭时，已绑定用户仍能登录，新用户被拒绝。
- 观猹 token 接口或用户信息接口失败时，不创建本站用户，不签发 JWT。
