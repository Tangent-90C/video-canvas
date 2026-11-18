import { describe, expect, test } from "bun:test";

import type { Capabilities } from "@/api/model/type";
import { fanoutCount, fanoutParam } from "@/utils/pricing/quote";

const caps = {
  params: {
    count: { type: "enum", label: "生成数量", fanout: true },
  },
} as unknown as Capabilities;

describe("fanout：只决定一次提交拆几个任务", () => {
  test("有 fanout 参数时取参数值，数字字符串也认", () => {
    expect(fanoutParam(caps)).toBe("count");
    expect(fanoutCount(caps, { count: 4 })).toBe(4);
    expect(fanoutCount(caps, { count: "2" })).toBe(2);
  });

  test("没有 fanout、值非法或不是正数时按 1 个", () => {
    expect(fanoutParam(undefined)).toBeUndefined();
    expect(fanoutCount(undefined, { count: 4 })).toBe(1);
    expect(fanoutCount(caps, { count: 0 })).toBe(1);
    expect(fanoutCount(caps, { count: "x" })).toBe(1);
  });
});
