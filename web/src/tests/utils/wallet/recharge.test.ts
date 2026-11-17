import { describe, expect, test } from "bun:test";

import {
  DEFAULT_WALLET_RECHARGE_AMOUNT,
  RECHARGE_PRESETS,
  parseRechargeAmount,
  rechargePhase,
} from "@/utils/wallet/recharge";

describe("钱包充值金额", () => {
  test("常用档位固定，默认选 30 元", () => {
    expect(RECHARGE_PRESETS).toEqual([10, 30, 50, 100]);
    expect(DEFAULT_WALLET_RECHARGE_AMOUNT).toBe(30);
  });

  test("自定义金额只接受 1–100000 的整数", () => {
    expect(parseRechargeAmount(" 30 ")).toBe(30);
    expect(parseRechargeAmount("1")).toBe(1);
    expect(parseRechargeAmount("100000")).toBe(100000);
    for (const value of ["", "0", "-1", "1.5", "100001", "abc"]) {
      expect(parseRechargeAmount(value)).toBeNull();
    }
  });
});

describe("充值状态", () => {
  test("pending 和 unknown 继续等，paid / failed / closed / refunded 各自落阶段", () => {
    expect(rechargePhase("pending")).toBe("pending");
    expect(rechargePhase("unknown")).toBe("pending");
    expect(rechargePhase("paid")).toBe("paid");
    expect(rechargePhase("failed")).toBe("failed");
    expect(rechargePhase("closed")).toBe("closed");
    expect(rechargePhase("refunded")).toBe("closed");
  });
});
