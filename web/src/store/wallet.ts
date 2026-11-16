import { create } from "zustand";

import {
  connectTokenPay,
  disconnectTokenPay,
  getWalletConnection,
  type WalletView,
} from "@/api/wallet";

/** 钱包连接状态的加载状态 */
export type WalletStatus = "idle" | "loading" | "ready" | "error";

type WalletApi = {
  getWalletConnection: typeof getWalletConnection;
  connectTokenPay: typeof connectTokenPay;
  disconnectTokenPay: typeof disconnectTokenPay;
};

type WalletState = {
  /** 当前钱包状态；未加载时为 null */
  view: WalletView | null;
  status: WalletStatus;
  /** 拉一次钱包状态；同一时刻多处触发只发一次请求（模型清单过滤要用它决定钱包模型可见性） */
  fetch: () => Promise<void>;
  /** 用授权回调带回的 flow + code 换取并保存 Key */
  connect: (flow: string, code: string) => Promise<WalletView>;
  /** 断开钱包 */
  disconnect: () => Promise<void>;
};

let inflight: Promise<void> | null = null;

/**
 * 创建钱包 store。对象里的方法可替换（测试用）
 * @param overrides 替换部分接口（测试用）
 */
export function createWalletStore(overrides: Partial<WalletApi> = {}) {
  const api: WalletApi = { getWalletConnection, connectTokenPay, disconnectTokenPay, ...overrides };

  return create<WalletState>((set, get) => ({
    view: null,
    status: "idle",
    fetch: () => {
      inflight ??= (async () => {
        set({ status: get().view ? "ready" : "loading" });
        try {
          const view = await api.getWalletConnection();
          set({ view, status: "ready" });
        } catch {
          set({ status: "error" });
        } finally {
          inflight = null;
        }
      })();
      return inflight;
    },
    connect: async (flow, code) => {
      const view = await api.connectTokenPay(flow, code);
      set({ view, status: "ready" });
      return view;
    },
    disconnect: async () => {
      await api.disconnectTokenPay();
      const current = get().view;
      set({
        view: current
          ? { ...current, connected: false, key_masked: "", connected_at: "" }
          : current,
      });
    },
  }));
}

export const useWalletStore = createWalletStore();
