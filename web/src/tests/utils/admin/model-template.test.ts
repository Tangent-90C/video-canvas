import { describe, expect, test } from "bun:test";

import { blankModelBody, defaultCapabilities, defaultDeadline } from "@/utils/admin/model-template";

describe("按种类预填的能力", () => {
  test("提示词字数上限：各种类新建时都预填 20000", () => {
    for (const kind of ["video", "image", "audio", "text", "agent"])
      expect(defaultCapabilities(kind).prompt?.max_length).toBe(20000);
  });

  test("视频模板有 duration 数字参数；文本没有生成方式", () => {
    expect(defaultCapabilities("video").params?.duration?.type).toBe("number");
    expect(defaultCapabilities("text").ops).toBeUndefined();
  });

  test("agent：有上下文和看图，没有生成方式、素材开关、生成参数、系统提示", () => {
    const caps = defaultCapabilities("agent");
    expect(caps.context).toEqual({ window: 200000, output: 8192 });
    expect(caps.vision).toBe(true);
    expect(caps.ops).toBeUndefined();
    expect(caps.params).toEqual({});
    expect(caps.system).toBeUndefined();
    expect(Object.values(caps.refs).every((r) => !r.on)).toBe(true);
    expect(defaultCapabilities("text").vision).toBeUndefined();
  });

  test("视频、图片带生成数量（fanout）参数", () => {
    expect(defaultCapabilities("video").params?.count?.fanout).toBe(true);
    expect(defaultCapabilities("image").params?.count?.options).toEqual([1, 2, 4]);
  });
});

describe("defaultDeadline / blankModelBody", () => {
  test("文本与 Agent 5 分钟，图片 10 分钟，视频与音频 30 分钟", () => {
    expect(defaultDeadline("text")).toBe("5m");
    expect(defaultDeadline("agent")).toBe("5m");
    expect(defaultDeadline("image")).toBe("10m");
    expect(defaultDeadline("video")).toBe("30m");
    expect(defaultDeadline("audio")).toBe("30m");
  });

  test("手动新增的起始正文是“待配置”半成品：没有能力、协议与定价，带预选渠道", () => {
    const body = blankModelBody("main", "gpt-x");
    expect(body).toMatchObject({
      key: "",
      kind: "",
      enabled: false,
      channels: [{ channel: "main", upstream_model: "gpt-x" }],
    });
    expect("capabilities" in body).toBe(false);
    expect("pricing" in body).toBe(false);
    expect(blankModelBody().channels).toEqual([{ channel: "", upstream_model: "" }]);
  });
});
