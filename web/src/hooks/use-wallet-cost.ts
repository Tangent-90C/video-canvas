import { useMemo } from "react";

import type { ModelInfo } from "@/api/model/type";
import { modelPriceLabel } from "@/utils/pricing/model-price";

/**
 * 钱包模型的价格文案：只展示 TokenDance 公开目录的实时价目。
 *
 * 下单前不再用历史成交均价估算金额；真实花费仍以生成后的 TokenDance 调用记录为准。
 *
 * @param model 当前钱包模型
 * @returns 公开价目文案；没有价目时返回「按 TokenDance 实际账单」
 */
export function useWalletPriceLabel(
  model: Pick<ModelInfo, "wallet" | "upstream_pricing"> | undefined,
) {
  return useMemo(() => (model?.wallet ? modelPriceLabel(model) : null), [model]);
}
