import { afterAll, beforeAll, describe, expect, test } from "bun:test";

import { installDom, type DomEnv } from "@/tests/helpers/dom";
import type { WalletTab as WalletTabType } from "@/pages/profile/wallet-tab";
import type { useWalletStore as useWalletStoreType } from "@/store/wallet";

/** 钱包卡片只展示公开 Key ID，不能出现授权 Key（完整值或掩码）。 */
let dom: DomEnv;
let WalletTab: typeof WalletTabType;
let useWalletStore: typeof useWalletStoreType;

beforeAll(async () => {
  dom = await installDom();
  WalletTab = (await import("@/pages/profile/wallet-tab")).WalletTab;
  useWalletStore = (await import("@/store/wallet")).useWalletStore;
});

afterAll(() => {
  useWalletStore.setState({ view: null, status: "idle", balance: null });
  dom.restore();
});

describe("WalletTab", () => {
  test("显示 TokenPay 后台可见的 Key ID，不显示授权 Key", async () => {
    useWalletStore.setState({
      status: "ready",
      view: {
        enabled: true,
        connected: true,
        app_url: "https://canvas.tan90.site",
        key_id: "01M7MCYZ45AYTH7GK0MZZRM85N",
        connected_at: "2026-11-18T08:00:00Z",
      },
      balance: { enabled: true, connected: true, balance_yuan: 10, used_yuan: 2, stale: false },
      fetch: async () => {},
      fetchBalance: async () => {},
      fetchUsage: async () => {},
      disconnect: async () => {},
    });

    const mounted = await dom.mount(<WalletTab />);
    await dom.flush();

    expect(mounted.container.textContent).toContain("Key ID：01M7MCYZ45AYTH7GK0MZZRM85N");
    expect(mounted.container.textContent).not.toContain("授权 Key");
    await mounted.unmount();
  });

  test("首次调用前没有 Key ID 时给出明确说明，不回退展示授权 Key", async () => {
    useWalletStore.setState({
      status: "ready",
      view: {
        enabled: true,
        connected: true,
        app_url: "https://canvas.tan90.site",
        key_id: "",
        connected_at: "2026-11-18T08:00:00Z",
      },
      balance: null,
      fetch: async () => {},
      fetchBalance: async () => {},
      fetchUsage: async () => {},
      disconnect: async () => {},
    });

    const mounted = await dom.mount(<WalletTab />);
    await dom.flush();

    expect(mounted.container.textContent).toContain("Key ID：首次调用后显示");
    expect(mounted.container.textContent).not.toContain("授权 Key");
    await mounted.unmount();
  });
});
