import type { ModelInfo } from "@/api/model/type";

/**
 * 模型选择器里的价格文案。
 *
 * 用户模型只应走 TokenPay 钱包；价格直接显示 TokenDance 公开目录的实时上游价目。
 * 上游价目拉取失败时明确按实际账单口径，不用历史成交均价或平台积分猜测。
 *
 * @param model 模型定价、钱包归属与上游价目
 */
export function modelPriceLabel(model: Pick<ModelInfo, "wallet" | "upstream_pricing">): string {
  if (!model.wallet) return "仅支持 TokenPay 钱包模型";
  return model.upstream_pricing?.label || "按 TokenDance 实际账单";
}
