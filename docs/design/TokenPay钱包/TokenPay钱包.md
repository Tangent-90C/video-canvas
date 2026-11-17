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

## 7.1 画布 Agent 与钱包的关系（2026-11-18 澄清）

`agent` 是和 text/image/video/audio 并列的第五种**模型种类**，与「渠道」是两个维度：TokenPay 是渠道，不是种类。agent 模型的特殊之处（`provider/modelcfg/types.go:45` 的注释）：

- **不走插件**，由 Go 的 LLM 网关按接口格式（`openai` / `gemini`）直接请求渠道——插件钩子是同步的、不能联网，所以 Agent 要流式 + 工具调用就只能绕过插件；
- **渠道同步不产 agent 模型**（`ai_config_channel_sync.go` 的规则表里没有 agent 分支），必须在后台手建；
- **不认用户钱包**：`service/agent/bridge.go` 的 `resolveModel` 直接读 `channel:<key>`，不做 `wallet:<uid>` 替换。所以绑在 TokenPay 渠道上的 agent 模型花的是**渠道自己的 Key**（运营者持有的产品方 Key），不是终端用户的钱包。归因头 `X-App-URL` 照常带上。

因此「用户用自己的 TokenDance 额度驱动画布 Agent」属于下表的二期项，当前部署的做法是：手建一个 agent 模型绑到 TokenPay 渠道（如 `agent-deepseek-v4.1-flash`，`api_format=openai`、`path_prefix=/v1`、`vision=true`），由运营者账号承担费用。

## 8. 明确不做（二期）

- Agent 对话（llmgateway）走用户钱包：llmgateway 按 token 计费，与冻结/结算体系不同，需要单独改造。
- 服务费比例收取、TokenDance 兑换码充值。
- 语音复刻、联网搜索等非四类能力；kling/vidu 走 TokenDance 网关的插件。

## 9. 平台积分在钱包场景的残留（2026-11-17 全站扫描）

**结论先行：本部署的平台积分体系对钱包用户是作废的，任何由「平台积分 × 汇率」得出的金额都是编造的数字。**
用户实测：seedream-5.0-lite 在 TokenDance 真实收费 ¥0.22/张，而按平台定价（4 积分）× 默认汇率（1 元 = 100 积分，`pricing_credits_per_yuan` 本部署未配置、后端写死默认值）算出来是 ¥0.04，差 5.5 倍。金额的唯一来源必须是 TokenDance 自己。

### 9.1 真实数据源：调用记录接口

`GET https://tokendance.space/portal/api/v1/usage`（Bearer = 用户自己的钱包 Key，与模型调用同一个 Key）：

| 字段 | 说明 |
| --- | --- |
| `items[].cost` | **真实花费，单位微元（1 元 = 1e6）** |
| `items[].model_slug` | **模型别名（如 `seedream-5.0-lite`），与本站模型的 `upstream_model` 一致——匹配认它** |
| `items[].model_id` | TokenDance 内部不透明 id（如 `01KMM9QH8VZX83GM06Q4A6JEF4`），**不是模型名，别拿来匹配**（v0.6.30 踩过） |
| `items[].created_at` | UTC ISO 8601 |
| `items[].is_success` | 是否成功 |
| `items[].input_tokens` / `output_tokens` | 图片模型也有（seedream 按输出 token 计费，16.4K → ¥0.22） |
| `id` | 分页游标 |

约束：按 `id` 倒序返回；`start_id` / `end_id` 游标翻页（只增 `offset` 会跨页重复或遗漏）；`window` 支持 `1h`/`24h`/`7d` 或 `YYYYMMDD-YYYYMMDD`；`model_id` / `is_success` 可精确过滤；`limit` 1–500；**同一用户 1QPS，超出 429**；官方暂无聚合接口（按天/小时/Top 模型统计未开放）。

### 9.2 待清理清单

**A. 折算汇率链路（数字错误，必须删）**

| # | 位置 | 现状 | 处置 |
| --- | --- | --- | --- |
| A1 | `backend/internal/service/user_wallet.go` `Balance` | 下发 `yuan_per_credit`（来自 `pricing_credits_per_yuan`，未配置时默认 100） | **已删**（v0.6.29）；「全局汇率」在本部署不存在 |
| A2 | `web/src/utils/pricing/quote.ts` `yuanEstimate()` | 积分 × 汇率 | **已删**（v0.6.29） |
| A3 | `web/src/utils/conversation/cell.ts` `recordWalletCost()` | v0.6.28 新增，用 A2 算记录花费 | **已删**（v0.6.29），改为 `utils/wallet/cost.ts` 的 `matchRecordCost` |
| A4 | `web/src/components/home/composer.tsx` | 输入卡片旁「≈¥X」 | **已改**（v0.6.29）：同模型历史均价 |
| A5 | `web/src/components/canvas/node-prompt-input.tsx` | 画布节点发送键旁「≈¥X」 | **已改**（v0.6.29）：同上 |
| A6 | `web/src/components/canvas/node-prompt-input.tsx` | `PromptFooter`/`SubmitPill` 同上 | **已改**（v0.6.29）：同上 |
| A7 | `web/src/components/conversation/record-view.tsx` | 记录卡片「≈¥X」 | **已改**（v0.6.29）：匹配到的真实花费（¥0.22，不再 ≈） |

**B. 平台积分价在钱包场景的展示（无意义，须屏蔽或替换）**

| # | 位置 | 现状 | 处置 |
| --- | --- | --- | --- |
| B1 | `web/src/components/canvas/node-prompt-input.tsx` | 节点模型下拉 `{priceLabel ?? "${credits} 积分"}`，**钱包模型漏屏蔽**，会显示「4 积分」「2 积分/秒」 | **已改**（v0.6.29）：钱包模型不显示价格行 |
| B2 | `web/src/components/canvas-editor/character-detail-dialog.tsx` | 音频模型下拉 `{label} · {priceLabel(pricing)}`，无 wallet 判断 | **已改**（v0.6.29）：钱包模型去掉价格 |
| B3 | `web/src/components/canvas-editor/character-node.tsx` | 「含音色」`extraCredits = lowestUnit(pricing)` | **已改**（v0.6.29）：钱包模型不计平台积分 |
| B4 | `web/src/components/canvas/node-prompt-input.tsx` | 「含音色」title 文案「形象 X 积分，音色约 Y 积分」 | **已改**（v0.6.29）：钱包模型改写为「约 ¥X」 |
| B5 | `web/src/pages/drama/workspace/assets-overlay.tsx` | 短剧批量生成「X 积分起」估算 | **已改**（v0.6.29）：钱包模型不计入积分估算 |
| B6 | `web/src/hooks/use-models.ts` | `toModelOption` 给所有模型（含钱包）算 `credits`/`priceLabel` | **已改**（v0.6.29）：钱包模型不算（治本），改带 `upstream_model` |

**C. 已正确、不用改**

- `components/home/composer-model.tsx:127`：钱包模型显示「用你自己的 TokenDance 额度」而非积分价。
- `pages/canvas/agent/model-popover.tsx:58`：已有 `!m.wallet` 屏蔽。
- `components/drama/video-model-menu.tsx:130`：只显示「钱包」徽标与连接提示，不显示积分。
- 右上角余额/已消耗（v0.6.26 / v0.6.27）：来自真实 balance 接口，正确。

### 9.3 实现（v0.6.29 完成）

1. **后端**：`UserWalletService.Usage(ctx, userID)` 代理 `/portal/api/v1/usage`（`limit=500&window=30d`，新 outbound purpose `tokenpay.usage`），进程内缓存 60s（与 balance 同一策略，规避 1QPS），失败返回旧缓存。handler `GET /wallet/tokenpay/usage`。
2. **前端 store**：`useWalletStore` 加 `usage` 状态与 `fetchUsage()`，刷新时机与 `fetchBalance` 一致（`useRemoteModels` 里一起拉、连上钱包后立刻拉、断开清空）。
3. **匹配（纯函数，可测）**：`web/src/utils/wallet/cost.ts` `matchRecordCost(record, tasks, usage, upstreamModel)` —— 对每个有任务的格子，在 usage 里找 `(model_slug || model_id) === upstream_model`、`created_at ∈ [submitted_at - 5 分钟容差, finished_at ?? deadline_at ?? submitted_at + 30 分钟]`、且未被其它格子占用的最小时间差项；`cost_yuan` 求和。一格格不上返回 null（界面显示「—」，不猜半个金额）。
4. **预估（下单前，usage 里还没有这条）**：`estimateCost(usage, upstreamModel, count)` = 同模型最近 10 次**成功**项的平均花费 × 数量。固定价模型（图片按张、音频按次）均值即真实单价。
5. **上游模型名**：`provider.ModelInfo` 新增公开字段 `upstream_model`（`ListModels` 从 `cfg.Channels[0].UpstreamModel` 取），前端 `toModelOption` 透传；模型清单的「不泄露内部字段」测试已同步更新（该字段现在是有意公开的）。
6. **限额**：`limit=500` 覆盖不到的历史记录显示「—」，不翻页（避免 1QPS 下把页面拖慢）。

匹配用到的字段：任务的 `submitted_at`（排队中还没有就退到 `created_at`）作为窗口起点，`finished_at` / `deadline_at` 作为终点。

### 9.4 验收

- 记录卡片显示 ¥0.22（与 TokenDance 门户「调用记录」逐条一致），不再是 ¥0.04。
- 输入卡片 / 画布节点下单前显示同模型历史均价。
- 全站再无「平台积分 × 汇率」得出的金额；钱包模型下拉、角色卡、短剧批量生成不出现积分。

## 10. 余额不足直达充值（2026-11-17）

钱包余额不足时不再只给一句「请充值后重试」，而是复用 TokenDance Agent 支付 API，在站内直接打开充值弹窗。

### 10.1 用户流程

1. 钱包任务失败且 `error_code=provider_balance` 时，画布节点、首页结果格显示「去充值」；桌宠失败气泡也带同名动作。
2. 右上角钱包浮层和个人中心钱包页始终提供充值入口，并显示当前 TokenDance 余额。
3. 弹窗默认 `¥30`，提供 `¥10 / ¥30 / ¥50 / ¥100` 档位和 `1–100000` 元自定义整数金额。
4. 创建会话后，桌面端把 `payment_url` 画成二维码；移动端只显示 `alipay_url` 跳转，不直接打开二维码内容。
5. 弹窗打开时每 3 秒查一次状态，页面重新可见时立即查；`paid / failed / closed / refunded / unknown` 或过期后停止。
6. 到账后刷新钱包余额。从生成失败进入时显示「已充值，继续生成」，用户确认后才重新提交原任务。

### 10.2 接口与安全

- `POST /api/v1/wallet/tokenpay/recharge/sessions`，body `{"amount":30}`。
  返回 `id / amount / status / payment_url / alipay_url / expires_at / paid_at`。
- `GET /api/v1/wallet/tokenpay/recharge/sessions/:id`，返回会话状态。
- 后端代理 `https://tokendance.space/portal/api/v1/payment/sessions`，Bearer 是当前用户自己的钱包 Key。
- 上游 `status_url` 不返回浏览器；查单始终由后端用固定接口 + 校验后的会话 ID 发起，防止任意 URL 代理。
- 充值请求走 `outbound.SensitiveClient`：仍记录 ext.call 的路径、状态和耗时，但 `payload.log` 不记录请求 / 响应体，避免支付链接落盘。
- 用户 Key、`status_url` 和支付链接不落库。支付完成为 `paid` 时清掉余额缓存，下一次余额查询直接打上游。
- 不新增充值订单表：同一 SPA 会话内关闭再打开保留进行中的会话，整页刷新不恢复；余额仍会在下一次查询时反映真实到账。

### 10.3 前端判定

- 任务快照新增 `wallet` 字段，HTTP、WebSocket 与断线对账共用。
- 只有 `wallet=true`、`status=failed`、`error_code=provider_balance` 同时成立才显示钱包充值；
  普通渠道的上游余额不足仍按平台故障处理，不能误导用户去充 TokenDance。
- 不做提交前按历史均价强制拦截：估算和 60 秒余额缓存都可能过期，以 TokenDance 实际返回的余额不足为准。
- 普通（BYOK）渠道任务的积分显示与冻结/结算不受影响。
