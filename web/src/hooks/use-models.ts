import { useCallback, useEffect, useMemo } from "react";

import type { ModelInfo } from "@/api/model/type";
import { useModelsStore, type ModelsEntry } from "@/store/models";
import { useWalletStore } from "@/store/wallet";
import type { ModelOption } from "@/types";
import { lowestUnit, priceLabel } from "@/utils/pricing/quote";

const EMPTY_ENTRY: ModelsEntry = { status: "idle", models: [], loadedAt: 0 };

/** 服务端模型 -> 输入框下拉认得的形状：key 就是节点里存的 model */
export const toModelOption = (info: ModelInfo): ModelOption => ({
  id: info.key,
  label: info.label,
  credits: lowestUnit(info.pricing),
  priceLabel: priceLabel(info.pricing),
  hint: info.hint,
  vendor: info.vendor,
  tags: info.tags,
  wallet: info.wallet,
});

/**
 * 读某种类的服务端模型清单（带缓存）。
 * status 让调用方分清「还在加载」「加载失败」和「加载成功但没有这个模型」——
 * 只有最后一种才该判定模型已下线。
 * 钱包渠道的模型只有连接了 TokenPay 钱包的用户可见：没连接时直接从清单里滤掉，也就不会被设成默认模型。
 */
export function useRemoteModels(kind: string, enabled = true) {
  const entry = useModelsStore((state) => state.byKind[kind]) ?? EMPTY_ENTRY;
  const load = useModelsStore((state) => state.load);
  const walletConnected = useWalletStore((state) => !!state.view?.connected);
  const fetchWallet = useWalletStore((state) => state.fetch);
  const fetchBalance = useWalletStore((state) => state.fetchBalance);

  useEffect(() => {
    if (enabled) void load(kind);
    // 钱包模型的可见性取决于连接状态；接口有去重，多处调用只发一次请求
    if (enabled) void fetchWallet();
    // 余额：右上角与预估花费要用；后端 60s 缓存，多个页面同时加载只打一次上游
    if (enabled && walletConnected) void fetchBalance();
  }, [enabled, kind, load, fetchWallet, fetchBalance, walletConnected]);

  const reload = useCallback(() => load(kind, true), [kind, load]);
  const options = useMemo(
    () => entry.models.filter((model) => !model.wallet || walletConnected).map(toModelOption),
    [entry.models, walletConnected],
  );

  return {
    status: entry.status,
    models: entry.models,
    options,
    reload,
  };
}
