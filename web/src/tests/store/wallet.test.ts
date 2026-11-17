import { afterEach, beforeEach, describe, expect, test } from "bun:test";

import { createWalletStore } from "@/store/wallet";

/** startConnect 要整页跳转（window.location.href）：node 测试环境没有 window，跳转目标记在这里 */
let redirected = "";
const savedWindow = (globalThis as { window?: unknown }).window;

beforeEach(() => {
  redirected = "";
  (globalThis as { window?: unknown }).window = {
    location: {
      pathname: "/home",
      search: "",
      get href() {
        return redirected;
      },
      set href(value: string) {
        redirected = value;
      },
    },
  };
});

afterEach(() => {
  (globalThis as { window?: unknown }).window = savedWindow;
});

/** 钱包 store 的替身接口：不发请求，只记录调用 */
function fakeApi() {
  const calls = {
    start: [] as string[],
    connect: [] as [string, string],
    disconnect: 0,
    view: 0,
  };
  return {
    calls,
    api: {
      getWalletConnection: async () => {
        calls.view += 1;
        return {
          enabled: true,
          connected: false,
          app_url: "https://canvas.tan90.site",
          key_masked: "",
          connected_at: "",
        };
      },
      connectTokenPay: async (flow: string, code: string) => {
        calls.connect.push([flow, code]);
        return {
          enabled: true,
          connected: true,
          app_url: "https://canvas.tan90.site",
          key_masked: "tk-…9",
          connected_at: "",
        };
      },
      disconnectTokenPay: async () => {
        calls.disconnect += 1;
      },
      startTokenPayAuth: async (next: string) => {
        calls.start.push(next);
        return { auth_url: "https://tokendance.space/auth?code_challenge=x" };
      },
    },
  };
}

describe("钱包 store：连接弹窗", () => {
  test("openPrompt 打开、closePrompt 关闭；状态没拉过时会先拉一次", async () => {
    const { api, calls } = fakeApi();
    const store = createWalletStore(api);
    store.getState().openPrompt();
    expect(store.getState().promptOpen).toBe(true);
    await store.getState().fetch();
    expect(calls.view).toBe(1);
    store.getState().closePrompt();
    expect(store.getState().promptOpen).toBe(false);
  });

  test("startConnect 带当前页面作为 next，成功后关弹窗并跳授权页", async () => {
    const { api, calls } = fakeApi();
    const store = createWalletStore(api);
    store.getState().openPrompt();
    await store.getState().startConnect("/canvas?tab=gen");
    expect(calls.start).toEqual(["/canvas?tab=gen"]);
    expect(store.getState().promptOpen).toBe(false);
    expect(redirected).toBe("https://tokendance.space/auth?code_challenge=x");
  });

  test("startConnect 传入窗口时在新标签页里导航，原页面不动", async () => {
    const { api, calls } = fakeApi();
    const store = createWalletStore(api);
    let tabHref = "";
    let closed = false;
    const win = {
      get closed() {
        return closed;
      },
      close: () => {
        closed = true;
      },
      location: {
        set href(value: string) {
          tabHref = value;
        },
      },
    } as unknown as Window;
    await store.getState().startConnect("/", win);
    expect(calls.start).toEqual(["/"]);
    expect(tabHref).toBe("https://tokendance.space/auth?code_challenge=x");
    expect(redirected).toBe(""); // 原页面没有跳走
  });

  test("startConnect 传入已关闭的窗口时降级为当前页跳转", async () => {
    const { api } = fakeApi();
    const store = createWalletStore(api);
    const win = { closed: true } as unknown as Window;
    await store.getState().startConnect("/", win);
    expect(redirected).toBe("https://tokendance.space/auth?code_challenge=x");
  });

  test("startConnect 不传 next 时用当前地址兜底", async () => {
    const { api, calls } = fakeApi();
    const store = createWalletStore(api);
    (globalThis as { window?: unknown }).window = {
      location: {
        pathname: "/home",
        search: "?tab=gen",
        get href() {
          return redirected;
        },
        set href(value: string) {
          redirected = value;
        },
      },
    };
    await store.getState().startConnect();
    expect(calls.start).toEqual(["/home?tab=gen"]);
  });

  test("授权回调换 Key 成功后自动关弹窗", async () => {
    const { api } = fakeApi();
    const store = createWalletStore(api);
    store.getState().openPrompt();
    const view = await store.getState().connect("flow-1", "code-1");
    expect(view.connected).toBe(true);
    expect(store.getState().promptOpen).toBe(false);
    expect(store.getState().status).toBe("ready");
  });

  test("fetchBalance 拉余额；失败时不影响（balance 保持 null）", async () => {
    const balance = {
      enabled: true,
      connected: true,
      balance_yuan: 12.5,
      used_yuan: 3,
      stale: false,
    };
    const store = createWalletStore({
      getWalletBalance: async () => balance,
      disconnectTokenPay: async () => {},
    } as never);
    await store.getState().fetchBalance();
    expect(store.getState().balance?.balance_yuan).toBe(12.5);
    expect(store.getState().balance?.used_yuan).toBe(3);

    // 断开钱包时清掉余额
    await store.getState().disconnect();
    expect(store.getState().balance).toBeNull();

    // 拉取失败不炸，balance 保持原值（null）
    const broken = createWalletStore({
      getWalletBalance: async () => {
        throw new Error("network");
      },
      disconnectTokenPay: async () => {},
    } as never);
    await broken.getState().fetchBalance();
    expect(broken.getState().balance).toBeNull();
  });

  test("fetchUsage 拉调用记录（真实花费的唯一来源）；断开时清掉", async () => {
    const usage = {
      enabled: true,
      connected: true,
      stale: false,
      items: [
        {
          model_id: "seedream-5.0-lite",
          created_at: "2026-11-17T12:00:07Z",
          success: true,
          cost_yuan: 0.22,
        },
      ],
    };
    const store = createWalletStore({
      getWalletUsage: async () => usage,
      disconnectTokenPay: async () => {},
    } as never);
    await store.getState().fetchUsage();
    expect(store.getState().usage?.items[0]?.cost_yuan).toBe(0.22);

    // 断开钱包时清掉调用记录
    await store.getState().disconnect();
    expect(store.getState().usage).toBeNull();

    // 拉取失败不炸，usage 保持原值（null）
    const broken = createWalletStore({
      getWalletUsage: async () => {
        throw new Error("network");
      },
      disconnectTokenPay: async () => {},
    } as never);
    await broken.getState().fetchUsage();
    expect(broken.getState().usage).toBeNull();
  });

  test("连接成功后自动拉一次余额", async () => {
    const calls = { balance: 0 };
    const store = createWalletStore({
      getWalletBalance: async () => {
        calls.balance += 1;
        return {
          enabled: true,
          connected: true,
          balance_yuan: 1,
          used_yuan: 0,
          stale: false,
        };
      },
      connectTokenPay: async () => ({
        enabled: true,
        connected: true,
        app_url: "https://canvas.tan90.site",
        key_masked: "tk-…1",
        connected_at: "",
      }),
      disconnectTokenPay: async () => {},
    } as never);
    await store.getState().connect("flow", "code");
    expect(calls.balance).toBe(1);
  });

  test("节流：已打开时 10 秒内的重复触发忽略，关掉后能再开（多个格子同时撞上未连接）", () => {
    const { api } = fakeApi();
    const store = createWalletStore(api);
    store.getState().openPrompt();
    expect(store.getState().promptOpen).toBe(true);
    store.getState().openPrompt(); // 已开着：直接忽略
    expect(store.getState().promptAt).toBeGreaterThan(0);
    store.getState().closePrompt();
    store.getState().openPrompt(); // 关过之后能再开
    expect(store.getState().promptOpen).toBe(true);
  });
});
