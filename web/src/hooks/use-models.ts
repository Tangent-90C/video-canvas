import { useCallback, useEffect, useMemo } from "react";

import type { ModelInfo } from "@/api/model/type";
import { useModelsStore, type ModelsEntry } from "@/store/models";
import { useWalletStore } from "@/store/wallet";
import type { ModelOption } from "@/types";
import { modelPriceLabel } from "@/utils/pricing/model-price";

const EMPTY_ENTRY: ModelsEntry = { status: "idle", models: [], loadedAt: 0 };

/** 服务端模型 -> 输入框下拉认得的形状：key 就是节点里存的 model */
export const toModelOption = (info: ModelInfo): ModelOption => ({
  id: info.key,
  label: info.label,
  // 价格文案只来自 TokenDance 公开实时价目。
  priceLabel: modelPriceLabel(info),
  hint: info.hint,
  vendor: info.vendor,
  tags: info.tags,
  wallet: info.wallet,
  upstream_model: info.upstream_model,
  capabilities: info.capabilities,
  kind: info.kind,
  upstream_pricing: info.upstream_pricing,
});

/**
 * 读某种类的服务端模型清单（带缓存）。
 * status 让调用方分清「还在加载」「加载失败」和「加载成功但没有这个模型」——
 * 只有最后一种才该判定模型已下线。
 * 钱包渠道模型始终返回，未连接时由各入口展示「连接 TokenPay 钱包」引导。
 */
export function useRemoteModels(kind: string, enabled = true) {
  const entry = useModelsStore((state) => state.byKind[kind]) ?? EMPTY_ENTRY;
  const load = useModelsStore((state) => state.load);
  const walletConnected = useWalletStore((state) => !!state.view?.connected);
  const fetchWallet = useWalletStore((state) => state.fetch);
  const fetchBalance = useWalletStore((state) => state.fetchBalance);
  const fetchUsage = useWalletStore((state) => state.fetchUsage);

  useEffect(() => {
    if (enabled) void load(kind);
    // 钱包模型的可见性取决于连接状态；接口有去重，多处调用只发一次请求
    if (enabled) void fetchWallet();
    // 余额与调用记录：右上角、预估花费、记录卡片的真实花费都要；后端 60s 缓存，多个页面同时加载只打一次上游
    if (enabled && walletConnected) {
      void fetchBalance();
      void fetchUsage();
    }
  }, [enabled, kind, load, fetchWallet, fetchBalance, fetchUsage, walletConnected]);

  const reload = useCallback(() => load(kind, true), [kind, load]);
  // 只向用户端暴露 TokenPay 钱包模型；未连接时保留清单，让用户可以点模型触发连接引导。
  const visibleModels = useMemo(
    () => entry.models.filter((model) => !!model.wallet),
    [entry.models],
  );
  const options = useMemo(() => visibleModels.map(toModelOption), [visibleModels]);

  return {
    status: entry.status,
    models: visibleModels,
    options,
    walletConnected,
    reload,
  };
}
