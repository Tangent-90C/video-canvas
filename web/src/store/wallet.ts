import { create } from "zustand";

import {
  connectTokenPay,
  disconnectTokenPay,
  getWalletConnection,
  startTokenPayAuth,
  type WalletView,
} from "@/api/wallet";

/** 授权跳转是整页的：弹窗打开后这个窗口内的重复触发忽略（多个格子同时撞上未连接） */
const PROMPT_THROTTLE = 10_000;

/** 钱包连接状态的加载状态 */
export type WalletStatus = "idle" | "loading" | "ready" | "error";

type WalletApi = {
  getWalletConnection: typeof getWalletConnection;
  connectTokenPay: typeof connectTokenPay;
  disconnectTokenPay: typeof disconnectTokenPay;
  startTokenPayAuth: typeof startTokenPayAuth;
};

type WalletState = {
  /** 当前钱包状态；未加载时为 null */
  view: WalletView | null;
  status: WalletStatus;
  /** 「连接钱包」弹窗是否打开；任何地方（生成被拦截、模型标记、个人中心）都能打开它 */
  promptOpen: boolean;
  /** 上次打开弹窗的时间戳（节流）；授权跳转期间状态不会变，重复触发直接忽略 */
  promptAt: number;
  /** 拉一次钱包状态；同一时刻多处触发只发一次请求（模型清单过滤要用它决定钱包模型可见性） */
  fetch: () => Promise<void>;
  /** 用授权回调带回的 flow + code 换取并保存 Key */
  connect: (flow: string, code: string) => Promise<WalletView>;
  /** 断开钱包 */
  disconnect: () => Promise<void>;
  /** 打开「连接钱包」弹窗（不跳转，用户点弹窗里的按钮才发起授权） */
  openPrompt: () => void;
  /** 关闭弹窗 */
  closePrompt: () => void;
  /**
   * 弹窗里点「连接」：发起授权。优先新开标签页（原页面不动，草稿不丢），
   * 被浏览器拦了（返回 null）就退回当前页整页跳转。
   * @param next 授权完成后（降级路径）要回来的页面
   * @param win 点击的同步栈里开好的空白标签页；没传或不支持时走整页跳转
   */
  startConnect: (next?: string, win?: Window | null) => Promise<void>;
};

let inflight: Promise<void> | null = null;

/**
 * 创建钱包 store。对象里的方法可替换（测试用）
 * @param overrides 替换部分接口（测试用）
 */
export function createWalletStore(overrides: Partial<WalletApi> = {}) {
  const api: WalletApi = {
    getWalletConnection,
    connectTokenPay,
    disconnectTokenPay,
    startTokenPayAuth,
    ...overrides,
  };

  return create<WalletState>((set, get) => ({
    view: null,
    status: "idle",
    promptOpen: false,
    promptAt: 0,
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
      set({ view, status: "ready", promptOpen: false });
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
    openPrompt: () => {
      // 状态还没拉过（或本站没启用）时先拉一次：弹窗要按 enabled / connected 显示对应内容
      if (get().status === "idle") void get().fetch();
      const now = Date.now();
      if (get().promptOpen && now - get().promptAt < PROMPT_THROTTLE) return;
      set({ promptOpen: true, promptAt: now });
    },
    closePrompt: () => set({ promptOpen: false }),
    startConnect: async (next, win) => {
      try {
        const start = await api.startTokenPayAuth(next ?? currentPath());
        set({ promptOpen: false });
        if (win && !win.closed) {
          // 新标签页方案：授权、登录、确认都在那边完成；原页面纹丝不动
          win.location.href = start.auth_url;
          return;
        }
        // 降级：开窗被拦（弹窗拦截 / 移动端限制），当前页整页跳转
        window.location.href = start.auth_url;
      } catch {
        // 发起失败（未启用 / 网络）：保持弹窗打开，用户可以看到错误并重试
        // 已经开出去的空白标签关掉，别留个空页
        if (win && !win.closed) win.close();
        throw new Error("发起授权失败，请稍后重试");
      }
    },
  }));
}

/** 当前页面地址：授权完成后（降级路径）回到这里，用户接着做原来的事 */
function currentPath() {
  if (typeof window === "undefined") return "/";
  return `${window.location.pathname}${window.location.search}`;
}

/**
 * 同源广播频道名：授权在新标签页里完成后，回调页通过它通知原页面「连上了」。
 * 原页面收到后刷新钱包状态即可——它从没离开，草稿、画布、滚动位置全在。
 */
export const WALLET_CHANNEL = "video-canvas:wallet";

/** 授权结果广播：新标签页完成后发给原页面 */
export type WalletAuthMessage = { type: "connected" } | { type: "failed"; reason: string };

export const useWalletStore = createWalletStore();
