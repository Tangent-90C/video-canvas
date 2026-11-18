# TokenPay 钱包设计（用户各自钱包）

> **范围**：TokenDance（TokenPay）是本站唯一的用户计费来源。每个终端用户在个人中心 OAuth 授权自己的 TokenDance 账号；所有生成任务、文本编排、营销环节和画布 Agent 对话都用用户自己的 Key 支付，平台不发行、不冻结、不结算任何积分。
>
> **日期和状态**：2026-11-18 完成单一计费重构；前后端与 Agent 测试已覆盖，真实 TokenDance 账号仍应按部署清单做一次充值、余额不足、授权失效与调用账单联调。
>
> **证据约定**：**事实**附仓库文件路径；**提案**是尚未实现的部分（见第 8 节「明确不做」）。

---

## 1. 设计摘要

- **问题**：旧实现同时存在平台积分和 TokenPay 用户钱包，容易把产品方渠道 Key、用户钱包 Key、平台积分价和上游真实账单混在一起，产生“用户白嫖产品方余额”或编造金额的风险。
- **目标**：
  - 用户在个人中心一键连接 TokenPay 钱包；任何模型入口在未连接时直接打开连接引导，不回退积分或其他渠道 Key。
  - 所有真实模型调用只用 `wallet:<uid>`；产品方渠道 Key 只允许用于拉取模型目录。
  - 调用量归因到本站（App URL），运营者从 TokenPay 后台按归因拿分润。
- **推荐结论**：
  1. **一个钱包渠道**：管理员建渠道时勾选「钱包渠道」，`base_url=https://tokendance.space/gateway`。TokenDance 全部端点同源（`/v1/responses`、`/v1/images/generations`、`/ark/v3/generations/tasks`、`/minimax/v1/t2a_v2`），Bearer 同源注入天然满足，四类能力共用这一个渠道。
  2. **模型绑定复用现有插件体系**：文本→`openai.js`、图像→`openai-image.js` 现成；视频/音频新写两个内置插件 `tokendance-video.js`（豆包 ark 协议同构）、`tokendance-tts.js`（minimax T2A v2）。
  3. **钱包 Key 复用 `ai_secrets`**：名字 `wallet:<user_id>`，AES-256-GCM 与渠道 Key 同一套主密钥；新表 `user_wallets` 只存展示元数据。
  4. **任务侧统一计费**：可启用模型必须绑定钱包渠道，创建任务时要求用户已连接钱包并标 `wallet=true`；执行时只允许 `loadSecret` 读取 `wallet:<uid>`。历史非钱包任务失败关闭，不能回退产品方渠道 Key。

## 2. 用户流程

1. 用户到「个人中心 → 钱包」，点「连接 TokenPay 钱包」。
2. 后端 `GET /api/v1/wallet/tokenpay/start` 生成 state + PKCE verifier（S256），存 Redis（绑定发起用户，TTL 10 分钟），返回 TokenDance 授权地址：
   `https://tokendance.space/auth?callback_url=<app_url>/wallet/tokenpay/callback?flow=<state>&next=<next>&code_challenge=...&code_challenge_method=S256&app_url=<app_url>&key_name=<key_name>`
3. 用户在 TokenDance 页面确认授权，上游带 `code` 回跳本站回调页（未登录时会先去登录再回来）。
4. 前端 `POST /api/v1/wallet/tokenpay/connect {flow, code}` → 后端用 code + verifier 换 Key（`POST https://tokendance.space/portal/api/v1/auth/keys`）→ Key 加密入库 + 元数据落库 → 跳回 `next`。
5. 连接后模型列表与 Agent 面板可用；生成和 Agent 对话都直接扣用户自己的 TokenDance 额度。断开钱包后入口保留连接引导，但不允许提交。

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

### 3.3 任务与 Agent 执行

- `generation_tasks.wallet bool` 标记统一钱包任务（`backend/internal/model/generation_task.go`）。新任务固定 `Wallet=true, Credits=0`。
- **创建**（`service/generation_task_create.go`）：模型必须绑定钱包渠道，并先查用户钱包连接；未连接返回 `ErrWalletNotConnected`。并发上限、幂等、营销风控等普通业务检查保留。
- **状态收尾**（`service/generation_task_store.go`）：不写冻结、结算或退款流水；成功/失败/取消只记录任务状态与用量。
- **凭证**（`provider/plugin/operation.go`）：真实任务只读 `wallet:<UserID>`。历史非钱包任务直接返回 `wallet_required`，不读 `channel:<key>`、不发上游请求；`channel:<key>` 仅用于目录同步。
- **文本与 Agent**：Agent、短剧文本、营销环节分别通过 LLM 网关或 stepkit 读取用户钱包 Key；非钱包快照直接失败关闭。
- **失败文案**（`worker/policy.go`）：余额不足给充值入口；授权失效给重新连接入口；历史非钱包任务提示重新用 TokenPay 钱包提交。
- **恢复动作头**：上游响应带 `TokenDance-Recovery-Action` 时，宿主优先按它分类：`top_up_balance` → `provider_balance`；`reauthorize_api_key` → `secret_unavailable`；`api_key_quota` → `key_quota`。

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

`UserWallet` 已注册进 `model.All()`，schema 一致性由 `TestMigrationsMatchModels` 保证。单一计费重构没有新增破坏性迁移；历史积分表、流水和旧 `pricing` / `credits` 字段继续保留。

### 4.2 用户侧接口（auth 组）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/wallet/tokenpay/start?next=` | 生成授权地址 |
| POST | `/api/v1/wallet/tokenpay/connect` | `{flow, code}` 换 Key 入库 |
| GET | `/api/v1/wallet/tokenpay/connection` | `{enabled, connected, app_url, key_masked, connected_at}` |
| GET | `/api/v1/wallet/tokenpay/balance` | `{enabled, connected, balance_yuan, used_yuan, stale}`（用户自己的 Key 查上游，60s 缓存，失败降级不报错） |
| GET | `/api/v1/wallet/tokenpay/usage?limit=` | 代理上游调用记录（真实花费），60s 缓存，见 §9 |
| POST | `/api/v1/wallet/tokenpay/recharge/sessions` | `{amount}` 创建 TokenDance 充值会话，见 §10 |
| GET | `/api/v1/wallet/tokenpay/recharge/sessions/:id` | 查询充值状态 |
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
| `APP_STORAGE_LOCAL_BASE_URL` | 可选。本地素材给上游的绝对地址前缀；不填时自动沿用 `APP_TOKENPAY_APP_URL`，避免把 `/files/...` 相对地址发给上游 | `https://lianjing.example.com` |

回调地址固定拼为 `<APP_TOKENPAY_APP_URL>/wallet/tokenpay/callback`，必须与浏览器实际访问地址一致（含 https）。

### 5.2 运营者接入步骤

1. **配置 env**：compose 里设 `APP_TOKENPAY_APP_URL`，重启后端。
2. **建钱包渠道**：管理后台 → AI 配置 → 渠道 → 新建：
   - `base_url` = `https://tokendance.space/gateway`
   - 勾选「钱包渠道」
   - HeadersJSON 填 `{"X-App-URL":"<你的站点地址>"}`（归因头）
   - API Key 填一个任意有效的 TokenDance Key（仅用于拉模型目录，用户请求不走它）
3. **同步全部模型**：钱包渠道页点「同步全部模型」，按上游 `supported_protocols` 自动建模型并启用：
   - 文本 → `openai.js`（Chat Completions）
   - 图像 → `tokendance-image.js`（Seedream / Qwen Image；含 Seedream 5.5、5.0 Pro）
   - Seedance → `tokendance-video.js`（`/ark/v3/generations/tasks`）
   - 可灵 V1/V2、Omni、数字人、视频特效 → `tokendance-kling-video.js`
   - Wan3.0 / HappyHorse → `tokendance-alibaba-video.js`
   - MiniMax H3 V2 → `tokendance-minimax-video.js`
   - 音频 → `tokendance-tts.js`
   - 同步会跳过渠道下已有的上游模型，可重复点击补齐上游新上架模型；不会修改已存在模型的运营配置。
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
- `backend/internal/tests/provider/plugin/wallet_secret_test.go`：真实 runner 装载 `tokendance-video.js`，钱包任务注入 `wallet:<uid>` 的 Bearer、不碰渠道密钥；历史非钱包任务失败关闭且不读取任何凭证。
- 全量 `go test ./...`（50 包，真实 PG/Redis）与前端 `typecheck`/`build` 通过；golangci-lint 由 CI（v2.14.0，`--new-from-merge-base=origin/master`）执行。

## 7. 上游同步注意事项

本功能在 `feat/tokenpay-wallet` 分支，`master` 只跟踪上游（ff-only 快进）。同步方式：`git fetch upstream && git rebase master`（高频小步，已开 rerere）。

为降低冲突面做的设计约束：

- **主体在新文件**（零冲突）：插件 ×2、`cache/oauth_flow.go`、`service/user_wallet.go`、`repository/user_wallet.go`、`handler/wallet.go`、`model/user_wallet.go`、迁移、前端 wallet api/store/页面 ×2、测试 ×3。
- **既有文件只有少量接缝**：任务/渠道快照的 Wallet 标记、所有真实执行入口的 wallet-only 校验、历史 `pricing` 宽容解析、钱包 API 路由。
- 上游若重构 worker、插件宿主或模型注册表，只需保持这些约束：真实调用只读 `wallet:<uid>`，目录同步才读 `channel:<key>`，历史非钱包任务失败关闭。

**上游地址的 `/portal` 前缀坑（v0.6.27 踩过）**：TokenDance 的开放平台接口（授权码交换、余额查询）都在 `https://tokendance.space/portal/api/v1/...` 下；一级域名下的 `/api/v1/...` 是官网前端路由，会返回 HTML（SPA fallback，HTTP 200、`Content-Type: text/html`），后端按 JSON 解析失败后表现为「余额显示 ¥0 + 可能不是最新」。验证方式：不带 Key 请求，`/portal/...` 返回 401 JSON，一级域名返回 200 HTML。

## 7.1 画布 Agent、文本模型与钱包（2026-11-18 更新）

`agent` 是和 text/image/video/audio 并列的第五种**模型种类**，TokenPay 是渠道，两者不是同一层概念。

- Agent 不走插件，由 Go 的 LLM 网关按 `openai` / `gemini` 接口格式直接请求 TokenPay。
- TokenPay 渠道一键同步覆盖 text/image/video/audio；agent 模型需要在后台手工创建并绑定 TokenPay 钱包渠道。
- Agent 对话、短剧文本、营销文本与视频理解都读取发起用户的 `wallet:<uid>`，不读取产品方渠道 Key；用户未连接时直接引导连接。
- 模型目录同步是唯一允许使用产品方渠道 Key 的路径；该 Key 不得进入生成、试跑、Agent 对话或任何用户请求。

## 8. 明确不做

- 平台积分充值、积分商城、积分余额、冻结、结算、退款、后台调账和定价设置。
- 产品方渠道 Key 代用户支付；它只用于同步模型目录。
- 非 TokenPay 钱包渠道承接用户生成或 Agent 对话。
- 服务费比例收取、TokenDance 兑换码充值；未落地的语音复刻等能力继续按当前插件目录判断。

## 9. 定价与真实账单

### 9.1 模型选择器：TokenDance 公开实时价目

- 后端 `service/upstream_pricing.go` 拉取 `https://tokendance.space/portal/api/models/all`，按上游 slug 归一化输入/输出 Token 价、图片张数价、视频秒价或 SKU 区间，只缓存成功的实时结果 5 分钟；缓存过期后刷新失败就省略价目，由前端显示「按 TokenDance 实际账单」，不拿过期缓存冒充实时价。
- `GET /api/v1/models` 只返回钱包模型，字段为 `key / kind / label / hint / vendor / tags / capabilities / wallet / upstream_model / upstream_pricing`。不再下发平台积分 `pricing`。
- Agent 模型选择器走同一份公开价目；`GET /api/v1/agent/models` 的 `price_label` 来自 `upstream_pricing`。
- 前端 `utils/pricing/model-price.ts` 只显示公开价目；拿不到价目时显示「按 TokenDance 实际账单」，不显示历史均价、不乘汇率、不读平台积分。
- 管理员模型编辑器和模型预检不展示或校验平台积分定价；历史 `pricing` 字段原样保留在旧正文中，但 `model_create` 和渠道同步不再写入它。

### 9.2 生成后的真实花费

`GET https://tokendance.space/portal/api/v1/usage`（Bearer = 用户自己的钱包 Key）：

| 字段 | 说明 |
| --- | --- |
| `items[].cost` | 真实花费，单位微元（1 元 = 1e6） |
| `items[].model_slug` | 上游模型别名，与本站 `upstream_model` 匹配 |
| `items[].model_id` | TokenDance 内部不透明 id，不作为模型名匹配 |
| `items[].created_at` | UTC ISO 8601 |
| `items[].is_success` | 是否成功 |
| `items[].input_tokens` / `output_tokens` | 文本、图片等调用都可能返回，仅用于对账与观测 |
| `id` | 分页游标 |

约束：按 `id` 倒序；`window` 支持 `1h` / `24h` / `7d` / `YYYYMMDD-YYYYMMDD`；`limit` 1–500；同一用户 1QPS。调用记录按模型、时间窗和未被占用记录匹配到本站任务；匹配不到时显示「—」，不猜半个金额。

### 9.3 禁止的旧算法

已删除或停用：

- `平台积分 × 全局汇率`、`credits_per_yuan`、历史成交均价。
- 模型清单里的平台积分价格、`pricing.cost` 和后台调价。
- Agent 预算、`quote_credits` 驱动的余额判断；这些字段只保留历史兼容值 0。
- 短剧、营销、分镜、图片/视频/音频生成中的积分冻结、结算和退款判断。

## 10. 平台积分退场与数据库兼容

### 10.1 已停用范围

- 生产路由不再注册平台充值、商城、支付回调、积分流水、后台调账、定价设置等接口；旧 handler/service 若为编译兼容保留，也不可被路由访问。
- 用户与后台接口不再返回积分余额、冻结额、积分消耗或调账结果；历史字段若仍有 JSON 兼容值，只允许为 0 或 `json:"-"`。
- 管理后台用户页、模型页、AI 助手提示词和工具输出不再提供积分定价或调账能力。
- `generation_tasks.wallet=false` 的历史任务失败关闭，不会回退到产品方渠道 Key。

### 10.2 兼容策略

- 不删除 `users` 的历史积分列、`user_credits`、`credit_ledger`、`payment_*` 或任务里的 `credits` / `pricing` 字段；旧库升级后仍可启动、读取历史和回滚镜像。
- `pricing` 是宽容的历史字段：旧结构、旧单价类型或未知嵌套字段不阻止模型读取、保存和启用；新代码不依赖它计价。
- 任务视图、短剧估价和 Agent 观测中的积分字段只作为旧客户端兼容值返回 0。
- 未新增破坏性迁移；`0040_user_wallet.sql` 是唯一的结构接缝：`user_wallets`、`ai_channels.wallet_enabled`、`generation_tasks.wallet`。

## 11. 余额不足直达充值

1. 钱包任务失败且 `error_code=provider_balance` 时，画布节点、首页结果格、桌宠失败气泡都显示「去充值」。
2. 右上角钱包浮层和个人中心钱包页始终提供充值入口，并显示当前 TokenDance 余额。
3. 弹窗默认 `¥30`，提供 `¥10 / ¥30 / ¥50 / ¥100` 档位和 `1–100000` 元自定义整数金额。
4. 创建会话后，桌面端把 `payment_url` 画成二维码；移动端只显示 `alipay_url` 跳转。
5. 弹窗打开时每 3 秒查一次状态，页面重新可见时立即查；终态或过期后停止。
6. 到账后刷新钱包余额。从生成失败进入时显示「已充值，继续生成」，用户确认后才重新提交原任务。

### 11.1 接口与响应解析

- `POST /api/v1/wallet/tokenpay/recharge/sessions`，body `{"amount":30}`。
- `GET /api/v1/wallet/tokenpay/recharge/sessions/:id`。
- 后端代理 `https://tokendance.space/portal/api/v1/payment/sessions`，Bearer 是当前用户自己的钱包 Key。
- 官方 Agent Payment 接口返回 `{"session": {...}}`；后端优先解包 `session`，同时兼容早期直接返回会话对象的实现。
- 上游 `status_url` 不返回浏览器；查单由后端用固定接口 + 校验后的会话 ID 发起。
- 充值请求走 `outbound.SensitiveClient`，不把支付链接或响应体写入日志，不持久化用户 Key、`status_url` 或支付链接。

## 12. 验收与观测

- `GET /api/v1/models` 和 `GET /api/v1/agent/models` 只出现钱包模型；定价来自 TokenDance 公开实时价目，缺价时明确显示实际账单口径。
- 未连接钱包时任何生成或 Agent 提交都返回 `40016`，前端就地打开连接弹窗。
- 生成、短剧文本、营销环节、Agent 对话、管理员试跑都只读用户钱包 Key；历史非钱包任务不读取产品方 Key。
- `TokenDance-Recovery-Action` 的充值/重新授权/周期额度动作可落到正确前端动作。
- 充值会话嵌套 `session` envelope 能正常解析，充值完成后余额刷新。
- 历史积分表和字段仍存在，服务升级、回滚和读取旧记录不被破坏。
