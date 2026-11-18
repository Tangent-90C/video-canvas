import { describe, expect, test } from "bun:test";

import type { ModelInfo } from "@/api/model/type";
import { modelPriceLabel } from "@/utils/pricing/model-price";

const model = (over: Partial<ModelInfo> = {}): ModelInfo => ({
  key: "m",
  kind: "image",
  label: "模型",
  pricing: { billing: "per_call", unit: 10 },
  capabilities: {
    refs: {
      image: { on: false, max: 0, max_mb: 0 },
      audio: { on: false, max: 0, max_mb: 0 },
      video: { on: false, max: 0, max_mb: 0 },
    },
    prompt: { max_length: 1000 },
  },
  ...over,
});

describe("modelPriceLabel", () => {
  test("非钱包旧模型不展示平台积分定价", () => {
    expect(modelPriceLabel(model())).toBe("仅支持 TokenPay 钱包模型");
    expect(
      modelPriceLabel(model({ pricing: { billing: "token", token: { in: 2, out: 8 } } })),
    ).not.toContain("积分");
  });

  test("钱包模型直接展示 TokenDance 上游公开价目", () => {
    expect(
      modelPriceLabel(
        model({
          wallet: true,
          upstream_model: "deepseek-v4.1-flash",
          upstream_pricing: {
            currency: "CNY",
            label: "输入 ¥0.8 / 输出 ¥3.2 / 百万 Token",
            input: 0.8,
            output: 3.2,
            unit: "millionTokens",
          },
        }),
      ),
    ).toBe("输入 ¥0.8 / 输出 ¥3.2 / 百万 Token");
  });

  test("上游价目拉取失败时不显示平台积分或历史均价", () => {
    const got = modelPriceLabel(
      model({ wallet: true, upstream_model: "up", upstream_pricing: undefined }),
    );
    expect(got).toBe("按 TokenDance 实际账单");
    expect(got).not.toContain("积分");
    expect(got).not.toContain("历史均价");
  });
});
