# 连镜文档

## 计费契约

平台积分体系已停用，TokenPay 钱包是唯一的用户计费来源：

- 模型生成、短剧文本、营销环节与画布 Agent 对话都使用发起用户自己的 TokenDance Key（`wallet:<uid>`）。
- TokenPay 钱包渠道不保存产品方 Key；上游模型目录是公开接口，用户调用只使用自己的 `wallet:<uid>`。
- 未连接钱包时返回 `40016` 并由前端就地打开连接引导；禁止回退积分或其他渠道 Key。
- 模型选择器展示 TokenDance 公开实时价目；拿不到价目时显示「按 TokenDance 实际账单」，不显示历史均价或平台积分。
- 历史积分表、流水和旧配置字段保留用于升级与回滚兼容；生产路由不再暴露积分充值、商城、余额、冻结、结算、退款、后台调账或定价设置接口。

完整设计、兼容层和充值协议见 [TokenPay 钱包设计](design/TokenPay钱包/TokenPay钱包.md)。

## 文档说明

- `design/` 下的 Markdown 记录设计和实施演进。带「计费状态（2026-11-18）」提示的旧文档，其计费段落只作历史背景，当前契约以本文和 TokenPay 设计为准。
- `design/**/*.html` 是交互原型快照，可能保留旧积分文案，不作为当前 API 或计费实现依据。
- `docker-production.md`、`docker-development.md` 和 `development.md` 是部署与开发入口。
