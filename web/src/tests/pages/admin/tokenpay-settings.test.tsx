import { afterAll, beforeAll, describe, expect, test } from "bun:test";

import type { ChannelOption } from "@/api/admin/ai/type.d";
import type { WalletSettings as WalletSettingsType } from "@/pages/admin/ai/tokenpay";
import { installDom, type DomEnv } from "@/tests/helpers/dom";

let dom: DomEnv;
let WalletSettings: typeof WalletSettingsType;

beforeAll(async () => {
  dom = await installDom();
  WalletSettings = (await import("@/pages/admin/ai/tokenpay")).WalletSettings;
});

afterAll(() => dom.restore());

describe("TokenPay 钱包设置", () => {
  test("不显示产品方 TokenDance API Key，也不把历史 secret_set 当成配置项", async () => {
    const channel = {
      key: "tokenpay",
      name: "TokenPay 钱包",
      base_url: "https://tokendance.space/gateway",
      headers: { "X-App-URL": "https://canvas.tan90.site" },
      rate_limit: { rps: 0, max_concurrency: 0, max_running: 0 },
      sort: 10,
      enabled: true,
      wallet_enabled: true,
      secret_set: true,
      secret_key_set: false,
      updated_by: 1,
      updated_at: "",
      created_at: "",
    } satisfies ChannelOption;

    const mounted = await dom.mount(
      <WalletSettings
        channel={channel}
        appURL="https://canvas.tan90.site"
        canWrite
        onSaved={() => {}}
      />,
    );
    await dom.flush();

    expect(mounted.container.textContent).toContain("已配置（启用中）");
    expect(mounted.container.textContent).not.toContain("TokenDance API Key");
    expect(mounted.container.textContent).not.toContain("产品方的 Key");
    expect(mounted.container.textContent).not.toContain("Key 已设置");
    expect(mounted.container.querySelector("#tokenpay-key")).toBeNull();
    await mounted.unmount();
  });
});
