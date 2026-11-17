# TokenPay 钱包设计（用户各自钱包）

> **范围**：接入 TokenDance（TokenPay）的「用户各自钱包」模式——每个终端用户在个人中心 OAuth 授权自己的 TokenDance 账号，钱包渠道上的生成任务用用户自己的 Key 支付，走用户自己的额度；平台不冻结、不扣积分，运营者纯拿 TokenPay 分润。
>
> **日期和状态**：2026-11-17，已实现（分支 `feat/tokenpay-wallet`，待真实账号联调）。
>
> **证据约定**：**事实**附仓库文件路径；**提案**是尚未实现的部分（见第 8 节「明确不做」）。

---

## 1. 设计摘要

- **问题**：想接入 TokenDance 吃返点，但平台统一渠道模式需要运营者垫调用成本、按积分转售；本站选用户各自钱包——用户用自己的 TokenDance 额度，运营者零成本拿分润。
- **目标**：
  - 用户在个人中心一键连接 TokenPay 钱包，连接后即可使用钱包模型，无其他配置。
  - 钱包任务不消耗平台积分；未连接钱包的用户看不到钱包模型。
  - 调用量归因到本站（App URL），运营者从 TokenPay 后台按归因拿分润。
- **推荐结论**：
  1. **一个钱包渠道**：管理员建渠道时勾选「钱包渠道」，`base_url=https://tokendance.space/gateway`。TokenDance 全部端点同源（`/v1/responses`、`/v1/images/generations`、`/ark/v3/generations/tasks`、`/minimax/v1/t2a_v2`），Bearer 同源注入天然满足，四类能力共用这一个渠道。
  2. **模型绑定复用现有插件体系**：文本→`openai.js`、图像→`openai-image.js` 现成；视频/音频新写两个内置插件 `tokendance-video.js`（豆包 ark 协议同构）、`tokendance-tts.js`（minimax T2A v2）。
  3. **钱包 Key 复用 `ai_secrets`**：名字 `wallet:<user_id>`，AES-256-GCM 与渠道 Key 同一套主密钥；新表 `user_wallets` 只存展示元数据。
  4. **任务侧最小接缝**：钱包渠道的模型 → 任务标 `wallet=true` → 跳过积分冻结/结算（仿 `IsTest` 分支）→ 执行时 `loadSecret` 按名字取用户 Key 替代渠道 Key。注入、同源校验、限流零改动。

## 2. 用户流程

1. 用户到「个人中心 → 钱包」，点「连接 TokenPay 钱包」。
2. 后端 `GET /api/v1/wallet/tokenpay/start` 生成 state + PKCE verifier（S256），存 Redis（绑定发起用户，TTL 10 分钟），返回 TokenDance 授权地址：
   `https://tokendance.space/auth?callback_url=<app_url>/wallet/tokenpay/callback?flow=<state>&next=<next>&code_challenge=...&code_challenge_method=S256&app_url=<app_url>&key_name=<key_name>`
3. 用户在 TokenDance 页面确认授权，上游带 `code` 回跳本站回调页（未登录时会先去登录再回来）。
4. 前端 `POST /api/v1/wallet/tokenpay/connect {flow, code}` → 后端用 code + verifier 换 Key（`POST https://tokendance.space/portal/api/v1/auth/keys`）→ Key 加密入库 + 元数据落库 → 跳回 `next`。
5. 连接后钱包模型在模型列表出现；生成时用用户 Key，不扣积分。断开钱包则模型隐藏、密钥删除。

## 3. 核心设计

### 3.1 钱包渠道（管理后台）

- `ai_channels.wallet_enabled bool` 标记钱包渠道（`backend/migrations/0040_user_wallet.sql`）。
- 勾选后：该渠道上的模型由发起用户自己的 Key 支付，渠道 Key 只用于 pull-models（拉模型目录）。
- 归因：渠道 HeadersJSON 配 `{"X-App-URL":"<站点地址>"}`，随每个上游请求携带；授权 Key 本身也带 App URL 归因（OAuth 参数 `app_url`）。
- `ChannelSnapshot.Wallet` 透传到快照（`backend/internal/provider/snapshot.go`），`ModelInfo.wallet` 透传到前端模型列表（`backend/internal/service/ai_config_registry.go`）。

### 3.2 用户钱包（OAuth）

- **flow 缓存**：`backend/internal/cache/oauth_flow.go`，`OAuthFlowStore` 接口，Redis 实现（Lua GET+DEL 原子消费）+ 内存降级；存 `{verifier, userID}`，TTL 10 分钟，消费即销毁（防重放），发起人不匹配按无效处理。
- **服务**：`backend/internal/service/user_wallet.go`：`Start / Connect / Disconnect / View / Enabled / Connected`。未配 `tokenpay.app_url` 时整个功能未启用（`ErrWalletDisabled`）。
- **Key 存储**：`AIConfigService.SetSecret(ctx, "wallet:<user_id>", key, userID)`；`user_wallets` 表存 `provider/app_url/key_masked`，重连覆盖（上游会创建新 Key）。
- **上游 HTTP**：`tokenPayHTTP = outbound.Client("tokenpay", ...)`，走统一出站客户端（arch 测试约束）。

### 3.3 任务执行

- `generation_tasks.wallet bool` 标记钱包任务（`backend/internal/model/generation_task.go`）。
- **创建**（`service/generation_task_create.go`）：钱包渠道模型先查钱包连接（`WalletChecker` 接口注入，未连接报 `ErrWalletNotConnected`）；任务 `Wallet=true, Credits=0`；`insertAndFreeze` 跳过 EnsureCredit/LockCredit/余额检查/冻结账本，保留并发上限、幂等、营销风控等检查。
- **结算**（`service/generation_task_store.go`）：`finish` 里 `t.IsTest || t.Wallet` 直接返回，不写 settle 流水。
- **凭证**（`provider/plugin/operation.go`）：`loadSecret` 按 `TaskRef.Wallet` 分支——钱包任务取 `wallet:<UserID>`，普通任务取渠道密钥；只换名字，注入/同源/脱敏登记照旧。
- **失败文案**（`worker/policy.go` `failureForWallet`）：钱包任务不提「积分已退回」；`provider_balance` →「你的 TokenDance 余额不足，请充值后重试（钱包任务不消耗平台积分）」；密钥缺失 →「请到个人中心重新连接」。渠道余额告警只对非钱包任务触发。
- **恢复动作头**（官方 OAuth 文档的硬性要求）：上游失败响应带 `TokenDance-Recovery-Action` 时，宿主（`plugin/executor.go` `classifyHTTPError`，在插件分类之前）直接按它定分类，对钱包和普通渠道任务都生效：`top_up_balance` → `provider_balance`；`reauthorize_api_key` → `secret_unavailable`；`api_key_quota` → `key_quota`（新分类），文案见 `worker/policy.go`。

### 3.4 协议插件

- `backend/plugins/tokendance-video.js`：仿 `doubao-video.js`（豆包 ark 协议同构），path 改 `/ark/v3/generations/tasks`，`mode:"async"`，轮询参数 `firstDelay:15, interval:10, maxInterval:20`。
- `backend/plugins/tokendance-tts.js`：minimax T2A v2（`POST /minimax/v1/t2a_v2`，`mode:"sync"`），`channelSettings` 声明 `voice_id`，`output_format:"url"` 走 URL 输出由宿主转存（TokenDance 不允许内联 base64 大响应）。

## 4. 数据与接口

### 4.1 迁移（`backend/migrations/0040_user_wallet.sql`）

```sql
CREATE TABLE user_wallets (
    user_id bigint PRIMARY KEY,
    provider varchar(32) NOT NULL,
    app_url varchar(512) NOT NULL DEFAULT '',
    key_masked varchar(64) NOT NULL DEFAULT '',
    created_at timestamptz,
    updated_at timestamptz
);
ALTER TABLE ai_channels ADD COLUMN wallet_enabled bool NOT NULL DEFAULT false;
ALTER TABLE generation_tasks ADD COLUMN wallet bool NOT NULL DEFAULT false;
```

`UserWallet` 已注册进 `model.All()`，schema 一致性由 `TestMigrationsMatchModels` 保证。

### 4.2 用户侧接口（auth 组）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/wallet/tokenpay/start?next=` | 生成授权地址 |
| POST | `/api/v1/wallet/tokenpay/connect` | `{flow, code}` 换 Key 入库 |
| GET | `/api/v1/wallet/tokenpay/connection` | `{enabled, connected, app_url, key_masked, connected_at}` |
| GET | `/api/v1/wallet/tokenpay/balance` | `{enabled, connected, balance_yuan, used_yuan, yuan_per_credit, stale}`（用户自己的 Key 查上游，60s 缓存，失败降级不报错） |
| DELETE | `/api/v1/wallet/tokenpay/connection` | 断开 |

### 4.3 错误码（`backend/internal/pkg/errcode/errcode.go`）

- `40015 ErrWalletDisabled`：本站未启用 TokenPay 钱包（409）。
- `40016 ErrWalletNotConnected`：请先在个人中心连接 TokenPay 钱包（402）。

### 4.4 公开配置

`GET /api/v1/auth/config` 返回 `tokenpay_enabled` / `tokenpay_app_url`，前端据此显隐钱包入口。

## 5. 部署与运营者配置

> TokenPay 是管理后台的**一等公民配置页**（`/admin/ai/tokenpay`，侧栏「TokenPay 钱包」）：
> 网关地址、X-App-URL 归因、TokenDance Key、启用开关在一张卡里，点「保存并开通」即建渠道
> （key 固定 `tokenpay`）+ 存 Key + 同步全部模型。通用渠道列表、渠道表单、模型弹窗都不出现钱包渠道。

### 5.1 部署 env

| 环境变量 | 说明 | 示例 |
| --- | --- | --- |
| `APP_TOKENPAY_APP_URL` | 本站对外地址。**必配**，不配则钱包功能整体关闭；同时用作 OAuth `app_url` 归因参数和回调基址 | `https://lianjing.example.com` |
| `APP_TOKENPAY_KEY_NAME` | 授权页展示的 Key 名称（默认「连镜」） | `连镜` |

回调地址固定拼为 `<APP_TOKENPAY_APP_URL>/wallet/tokenpay/callback`，必须与浏览器实际访问地址一致（含 https）。

### 5.2 运营者接入步骤

1. **配置 env**：compose 里设 `APP_TOKENPAY_APP_URL`，重启后端。
2. **建钱包渠道**：管理后台 → AI 配置 → 渠道 → 新建：
   - `base_url` = `https://tokendance.space/gateway`
   - 勾选「钱包渠道」
   - HeadersJSON 填 `{"X-App-URL":"<你的站点地址>"}`（归因头）
   - API Key 填一个任意有效的 TokenDance Key（仅用于拉模型目录，用户请求不走它）
3. **拉模型并绑定**：从该渠道拉取模型目录，按能力绑定插件：
   - 文本 → `openai.js`（`/v1/responses`）
   - 图像 → `openai-image.js`（`/v1/images/generations`）
   - 视频 → `tokendance-video.js`（seedance，`/ark/v3/generations/tasks`）
   - 音频 → `tokendance-tts.js`（minimax T2A，`/minimax/v1/t2a_v2`）
   - 每个模型的「模型协议设置」按上游要求填（如 TTS 的 `voice_id`）。
4. **走通 DryRun** 再发布模型。
5. **验证归因**：用自己的 TokenDance 账号连接并生成一单，到 TokenPay 后台确认调用量与 App URL 归因正确。

### 5.3 真实账号联调清单

- [ ] 连接：`/profile` 钱包页 → 授权 → 回调落地 → 显示 Key 掩码
- [ ] 四类任务各生成一单：状态推进、产物落资产库、积分余额不变
- [ ] 异常：TokenDance 余额不足 / 断开钱包后再生成 → 提示文案正确
- [ ] TokenPay 后台归因调用量 +1（App URL 正确）

## 6. 测试

- `backend/internal/tests/cache/oauth_flow_test.go`：state 一次性、发起人校验、过期（注入时钟）。
- `backend/internal/tests/service/user_wallet_test.go`：授权地址参数（PKCE challenge、回调、next 双层编码）、connect 成功/失败路径（错 state、错发起人、上游无 Key）、断开清理、未启用形态。
- `backend/internal/tests/provider/plugin/wallet_secret_test.go`：真实 runner 装载 `tokendance-video.js`，钱包任务注入 `wallet:<uid>` 的 Bearer、不碰渠道密钥；普通任务相反。
- 全量 `go test ./...`（50 包，真实 PG/Redis）与前端 `typecheck`/`build` 通过；golangci-lint 由 CI（v2.14.0，`--new-from-merge-base=origin/master`）执行。

## 7. 上游同步注意事项

本功能在 `feat/tokenpay-wallet` 分支，`master` 只跟踪上游（ff-only 快进）。同步方式：`git fetch upstream && git rebase master`（高频小步，已开 rerere）。

为降低冲突面做的设计约束：

- **主体在新文件**（零冲突）：插件 ×2、`cache/oauth_flow.go`、`service/user_wallet.go`、`repository/user_wallet.go`、`handler/wallet.go`、`model/user_wallet.go`、迁移、前端 wallet api/store/页面 ×2、测试 ×3。
- **既有文件只有几行级接缝**（重做面）：`loadSecret` 分支、`TaskRef.Wallet`/`ChannelSnapshot.Wallet`/`ModelInfo.wallet` 字段、`insertAndFreeze`/`finish` 的钱包跳过、渠道 DTO 一个 bool、`auth/config` 两个返回值、路由一组。
- 上游若重构 worker/executor 或冻结/结算流程，只需要重接这些接缝点；`IsTest` 分支是天然参照。

**上游地址的 `/portal` 前缀坑（v0.6.27 踩过）**：TokenDance 的开放平台接口（授权码交换、余额查询）都在 `https://tokendance.space/portal/api/v1/...` 下；一级域名下的 `/api/v1/...` 是官网前端路由，会返回 HTML（SPA fallback，HTTP 200、`Content-Type: text/html`），后端按 JSON 解析失败后表现为「余额显示 ¥0 + 可能不是最新」。验证方式：不带 Key 请求，`/portal/...` 返回 401 JSON，一级域名返回 200 HTML。

## 8. 明确不做（二期）

- Agent 对话（llmgateway）走用户钱包：llmgateway 按 token 计费，与冻结/结算体系不同，需要单独改造。
- 服务费比例收取、TokenDance 兑换码/支付会话充值引导。
- 语音复刻、联网搜索等非四类能力；kling/vidu 走 TokenDance 网关的插件。
