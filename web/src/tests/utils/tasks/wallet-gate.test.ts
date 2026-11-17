import { describe, expect, test } from "bun:test";

import { isWalletNotConnected, WALLET_NOT_CONNECTED_CODE } from "@/utils/tasks/gateway";

/** 后端 errcode.ErrWalletNotConnected 的错误形状（ApiError：code + status） */
const walletError = { code: 40016, status: 402, message: "请先在个人中心连接 TokenPay 钱包" };

describe("钱包未连接的识别", () => {
  test("40016 认得出，且不会再被当成积分不足（402 兜底）", () => {
    expect(WALLET_NOT_CONNECTED_CODE).toBe(40016);
    expect(isWalletNotConnected(walletError)).toBe(true);
    // 其它错误不误伤
    expect(isWalletNotConnected({ code: 40001, status: 402 })).toBe(false);
    expect(isWalletNotConnected({ code: "HTTP_402", status: 402 })).toBe(false);
    expect(isWalletNotConnected(null)).toBe(false);
    expect(isWalletNotConnected("network")).toBe(false);
  });
});
