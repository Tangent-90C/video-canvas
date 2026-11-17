import { describe, expect, test } from "bun:test";

import { mapWalletRecharge } from "@/api/wallet";

describe("充值会话映射", () => {
  test("支付链接缺失转 null，RFC3339 时间转毫秒", () => {
    expect(
      mapWalletRecharge({
        id: "sess-1",
        amount: 30,
        status: "paid",
        expires_at: "2027-01-15T08:00:00Z",
        paid_at: "2027-01-15T07:45:00Z",
      }),
    ).toEqual({
      id: "sess-1",
      amount: 30,
      status: "paid",
      paymentUrl: null,
      alipayUrl: null,
      expiresAt: Date.parse("2027-01-15T08:00:00Z"),
      paidAt: Date.parse("2027-01-15T07:45:00Z"),
    });
  });
});
