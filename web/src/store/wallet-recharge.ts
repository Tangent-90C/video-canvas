import { create } from "zustand";

import { createWalletRecharge, getWalletRecharge, type WalletRechargeSession } from "@/api/wallet";
import { useWalletStore } from "@/store/wallet";
import { ApiError } from "@/utils/requests/request";
import {
  DEFAULT_WALLET_RECHARGE_AMOUNT,
  parseRechargeAmount,
  rechargePhase,
  type WalletRechargePhase,
} from "@/utils/wallet/recharge";

/** 打开充值弹窗时可带的业务回调 */
export type WalletRechargeOpenOptions = {
  /** 支付到账后，用户点“继续生成”时执行的原任务重试 */
  onPaid?: () => void | Promise<void>;
};

type WalletRechargeApi = {
  createWalletRecharge: typeof createWalletRecharge;
  getWalletRecharge: typeof getWalletRecharge;
};

type WalletRechargeState = {
  /** 弹窗是否打开 */
  open: boolean;
  /** 当前阶段 */
  phase: WalletRechargePhase;
  /** 金额输入框原文；档位也写入这里 */
  amountInput: string;
  /** 当前充值会话；金额阶段为 null */
  session: WalletRechargeSession | null;
  /** 金额校验或主动查单失败的提示 */
  errorMessage: string;
  /** 到账后是否还有原任务可以继续 */
  hasContinuation: boolean;
  /** 打开弹窗；进行中的会话会原样保留 */
  openRecharge: (options?: WalletRechargeOpenOptions) => void;
  /** 关闭弹窗，但保留会话 */
  close: () => void;
  /** 修改金额并清掉旧错误 */
  setAmountInput: (value: string) => void;
  /** 创建充值会话 */
  create: () => Promise<void>;
  /** 查询当前会话状态；silent=false 用于用户主动查单 */
  refresh: (silent?: boolean) => Promise<void>;
  /** 会话过期：若还在等待付款，转成关闭态 */
  markExpired: () => void;
  /** 回到金额阶段重新创建 */
  reset: () => void;
  /** 到账后继续原任务；没有回调时只完成并回到金额阶段 */
  continueAfterPaid: () => Promise<void>;
};

/** 新的金额阶段状态 */
const amountState = () => ({
  phase: "amount" as const,
  amountInput: String(DEFAULT_WALLET_RECHARGE_AMOUNT),
  session: null,
  errorMessage: "",
  hasContinuation: false,
});

/**
 * 创建充值 store。API 可替换，便于测试完整状态流转。
 * @param overrides 替换部分 API
 */
export function createWalletRechargeStore(overrides: Partial<WalletRechargeApi> = {}) {
  const api: WalletRechargeApi = { createWalletRecharge, getWalletRecharge, ...overrides };
  /** 到账后继续原任务的回调不上屏，只放在 store 内存里 */
  let onPaid: (() => void | Promise<void>) | null = null;

  const settle = (
    session: WalletRechargeSession,
    set: (value: Partial<WalletRechargeState>) => void,
  ) => {
    set({ session, phase: rechargePhase(session.status), errorMessage: "" });
    if (session.status === "paid") void useWalletStore.getState().fetchBalance();
  };

  return create<WalletRechargeState>((set, get) => ({
    open: false,
    ...amountState(),

    openRecharge: (options) => {
      if (options?.onPaid) onPaid = options.onPaid;
      set({ open: true, hasContinuation: !!onPaid, errorMessage: "" });
    },
    close: () => set({ open: false }),
    setAmountInput: (value) => set({ amountInput: value, errorMessage: "" }),

    create: async () => {
      if (get().phase === "creating") return;
      const amount = parseRechargeAmount(get().amountInput);
      if (amount === null) {
        set({ phase: "amount", errorMessage: "请输入 1–100000 元的整数金额" });
        return;
      }
      set({ phase: "creating", errorMessage: "" });
      try {
        settle(await api.createWalletRecharge(amount), set);
      } catch (error) {
        set({
          phase: "amount",
          errorMessage: error instanceof Error ? error.message : "创建充值失败",
        });
        if (error instanceof ApiError && error.code === 40016) {
          set({ open: false });
          useWalletStore.getState().openPrompt();
        }
      }
    },

    refresh: async (silent = true) => {
      const current = get().session;
      if (!current || get().phase !== "pending") return;
      try {
        const next = await api.getWalletRecharge(current.id, silent);
        settle(
          {
            ...next,
            paymentUrl: next.paymentUrl ?? current.paymentUrl,
            alipayUrl: next.alipayUrl ?? current.alipayUrl,
          },
          set,
        );
      } catch (error) {
        if (!silent) {
          set({ errorMessage: error instanceof Error ? error.message : "查询充值状态失败" });
        }
      }
    },

    markExpired: () => {
      const current = get().session;
      if (!current || get().phase !== "pending") return;
      set({
        phase: "closed",
        errorMessage: "充值会话已过期，请重新创建",
        session: { ...current, status: "closed" },
      });
    },
    reset: () => set({ open: true, ...amountState() }),
    continueAfterPaid: async () => {
      if (get().phase !== "paid") return;
      const callback = onPaid;
      onPaid = null;
      set({ ...amountState(), open: false });
      await callback?.();
    },
  }));
}

export const useWalletRechargeStore = createWalletRechargeStore();

/**
 * 从任意入口打开充值弹窗。
 * @param options 可选的支付后继续回调
 */
export const openWalletRecharge = (options?: WalletRechargeOpenOptions) =>
  useWalletRechargeStore.getState().openRecharge(options);
