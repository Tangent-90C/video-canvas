import { afterEach, beforeEach, describe, expect, test } from "bun:test";

import type { WalletRechargeSession } from "@/api/wallet";
import { createWalletRechargeStore } from "@/store/wallet-recharge";
import { useWalletStore } from "@/store/wallet";

let originalFetchBalance = useWalletStore.getState().fetchBalance;

beforeEach(() => {
  originalFetchBalance = useWalletStore.getState().fetchBalance;
  useWalletStore.setState({ fetchBalance: async () => {} });
});

afterEach(() => {
  useWalletStore.setState({ fetchBalance: originalFetchBalance });
});

const session = (patch: Partial<WalletRechargeSession> = {}): WalletRechargeSession => ({
  id: "sess-1",
  amount: 30,
  status: "pending",
  paymentUrl: "weixin://pay/sess-1",
  alipayUrl: "alipays://pay/sess-1",
  expiresAt: Date.now() + 60_000,
  paidAt: null,
  ...patch,
});

describe("钱包充值 store", () => {
  test("创建成功后进入等待付款，查单保留创建响应里的支付链接", async () => {
    const calls = { create: 0, get: 0, balance: 0 };
    const store = createWalletRechargeStore({
      createWalletRecharge: async (amount) => {
        calls.create += 1;
        return session({ amount });
      },
      getWalletRecharge: async () => {
        calls.get += 1;
        return {
          id: "sess-1",
          amount: 30,
          status: "paid",
          expiresAt: Date.now() + 60_000,
          paidAt: Date.now(),
        };
      },
    });
    useWalletStore.setState({
      fetchBalance: async () => {
        calls.balance += 1;
      },
    });
    store.getState().openRecharge();
    await store.getState().create();
    expect(calls.create).toBe(1);
    expect(store.getState().phase).toBe("pending");
    expect(store.getState().session?.paymentUrl).toBe("weixin://pay/sess-1");

    await store.getState().refresh(true);
    expect(calls.get).toBe(1);
    expect(calls.balance).toBe(1);
    expect(store.getState().phase).toBe("paid");
    expect(store.getState().session?.paymentUrl).toBe("weixin://pay/sess-1");
  });

  test("自定义金额非法时不发请求，并在原地显示错误", async () => {
    let called = false;
    const store = createWalletRechargeStore({
      createWalletRecharge: async () => {
        called = true;
        return session();
      },
      getWalletRecharge: async () => session(),
    });
    store.getState().openRecharge();
    store.getState().setAmountInput("1.5");
    await store.getState().create();
    expect(called).toBe(false);
    expect(store.getState().phase).toBe("amount");
    expect(store.getState().errorMessage).toContain("整数");
  });

  test("关闭后再打开保留进行中的会话；付款后继续生成只回调一次并回到金额页", async () => {
    let continued = 0;
    const store = createWalletRechargeStore({
      createWalletRecharge: async () => session({ status: "paid", paidAt: Date.now() }),
      getWalletRecharge: async () => session({ status: "paid", paidAt: Date.now() }),
    });
    store.getState().openRecharge({
      onPaid: () => {
        continued += 1;
      },
    });
    await store.getState().create();
    expect(store.getState().phase).toBe("paid");

    store.getState().close();
    store.getState().openRecharge();
    expect(store.getState().phase).toBe("paid");
    await store.getState().continueAfterPaid();
    expect(continued).toBe(1);
    expect(store.getState().open).toBe(false);
    expect(store.getState().phase).toBe("amount");

    await store.getState().continueAfterPaid();
    expect(continued).toBe(1);
  });
});
