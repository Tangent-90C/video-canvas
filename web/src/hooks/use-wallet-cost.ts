import { useMemo } from "react";

import { useWalletStore } from "@/store/wallet";
import { estimateCost, formatYuan } from "@/utils/wallet/cost";

/**
 * 钱包模型下单前的预估花费（元）：同模型最近几次真实花费的均价 × 生成数量。
 *
 * 为什么不用平台积分价换算：钱包任务不扣平台积分，用户在自己 TokenDance 账号里付的钱
 * 和平台积分没有关系（曾按 4 积分 × 0.01 显示 ¥0.04，真实收费 ¥0.22，差 5.5 倍）。
 * 这条记录还没发生，调用记录里没有它，只能用同模型的历史均价顶一下——
 * 固定价模型（图片按张、音频按次）的均值就是真实单价。
 *
 * @param upstreamModel 上游模型名；空串（普通渠道模型 / 模型已下线）返回 null
 * @param count 生成数量
 * @returns 金额文案（如「¥0.22」）；拿不到返回 null，界面显示「—」
 */
export function useWalletCostEstimate(upstreamModel: string | undefined, count = 1) {
  const items = useWalletStore((state) => state.usage?.items);
  return useMemo(() => {
    const yuan = estimateCost(items ?? [], upstreamModel ?? "", count);
    return yuan === null ? null : formatYuan(yuan);
  }, [items, upstreamModel, count]);
}
