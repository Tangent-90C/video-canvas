import { describe, expect, test } from "bun:test";

import type { ChannelView } from "@/api/admin/ai/type";
import { fieldOfPath, modelChecks } from "@/pages/admin/ai/models/model-fields";
import { resolveModelChannel } from "@/utils/admin/model-channel";

describe("fieldOfPath：校验问题定位到字段", () => {
  test("接口格式校验定位到基本信息的格式下拉", () => {
    expect(fieldOfPath("channels[0].api_format")).toEqual({
      tab: "basic",
      id: "model-api-format",
    });
  });

  test("路径前缀校验定位到基本信息的路径前缀输入框", () => {
    expect(fieldOfPath("channels[0].path_prefix")).toEqual({
      tab: "basic",
      id: "model-path-prefix",
    });
  });

  test("协议问题定位到“能力与协议”区块，渠道问题仍定位到所属渠道", () => {
    expect(fieldOfPath("channels[0].protocol")).toEqual({ tab: "basic", id: "model-protocol" });
    expect(fieldOfPath("channels[0].protocol.settings.region")).toEqual({
      tab: "basic",
      id: "model-protocol",
    });
    expect(fieldOfPath("channels[0].channel")).toEqual({ tab: "basic", id: "model-channel" });
    expect(fieldOfPath("channels[0].upstream_model")).toEqual({
      tab: "basic",
      id: "model-upstream",
    });
    expect(fieldOfPath("channels")).toEqual({ tab: "basic", id: "model-channel" });
  });

  test("其他顶层字段按页签分", () => {
    expect(fieldOfPath("kind")?.tab).toBe("basic");
    expect(fieldOfPath("capabilities.params.a")?.tab).toBe("params");
    expect(fieldOfPath("pricing.unit")).toBeNull();
    expect(fieldOfPath("nope")).toBeNull();
  });
});

describe("modelChecks：启用前待办", () => {
  const channel = {
    key: "main",
    name: "主渠道",
    enabled: true,
    wallet_enabled: true,
    secret_set: true,
  } as ChannelView;

  test("待配置的模型：没选能力、没选协议都列出（协议问题挂在 channels[0].protocol）", () => {
    const body = {
      key: "m",
      label: "L",
      kind: "",
      channels: [{ channel: "main", upstream_model: "u" }],
    };
    const checks = modelChecks(body, resolveModelChannel(body, [channel], []), []);
    expect(checks.filter((item) => item.tab === "basic").map((item) => item.path)).toEqual([
      "kind",
    ]);
    const text = { ...body, kind: "text" };
    const textChecks = modelChecks(text, resolveModelChannel(text, [channel], []), []);
    expect(textChecks.find((item) => item.path === "channels[0].protocol")?.text).toBe(
      "该模型还没有选择调用协议",
    );
  });

  test("清单没加载好时不对渠道与协议下结论", () => {
    const body = {
      key: "m",
      label: "L",
      kind: "text",
      channels: [{ channel: "x", upstream_model: "u" }],
    };
    const checks = modelChecks(body, resolveModelChannel(body, [], []), [], false);
    expect(checks.filter((item) => item.tab === "basic")).toEqual([]);
  });

  test("后端返回的问题按路径归到页签", () => {
    const body = {
      key: "m",
      label: "L",
      kind: "agent",
      channels: [{ channel: "main", upstream_model: "u" }],
    };
    const checks = modelChecks(body, resolveModelChannel(body, [channel], []), [
      { path: "channels[0].protocol", message: "Agent 模型不需要选择协议" },
      { path: "pricing.unit", message: "价格不对" },
    ]);
    expect(checks.some((item) => item.path === "pricing.unit")).toBe(false);
  });
});

describe("modelChecks：红点（阻止保存）与黄点（建议）", () => {
  const channel = {
    key: "main",
    name: "主渠道",
    enabled: true,
    wallet_enabled: true,
    secret_set: true,
  } as ChannelView;
  const base = {
    key: "m",
    label: "L",
    kind: "image",
    deadline: "10m",
    channels: [{ channel: "main", upstream_model: "u" }],
    capabilities: {
      ops: ["generate"],
      refs: {},
      prompt: { max_length: 2000 },
      params: { quality: { type: "enum", label: "清晰度", options: ["1k", "2k"], spec: true } },
    },
    pricing: {
      billing: "per_call",
      unit: 3,
      tiers: [{ on: true, when: { quality: "2k" }, unit: 5 }],
    },
  };
  const run = (body: Record<string, unknown>, issues: { path: string; message: string }[] = []) =>
    modelChecks(body, resolveModelChannel(body, [channel], []), issues, false);

  test("配置一致时没有红点", () => {
    expect(run(base).filter((item) => item.level === "error")).toEqual([]);
  });

  test("提示词字数上限超出范围、自定义超时格式不对都是红点，落在能力与参数页签", () => {
    const body = {
      ...base,
      deadline: "abc",
      capabilities: { ...base.capabilities, prompt: { max_length: 0 } },
    };
    const errors = run(body).filter((item) => item.level === "error");
    expect(errors.map((item) => [item.tab, item.path])).toEqual([
      ["params", "deadline"],
      ["params", "capabilities.prompt"],
    ]);
  });

  test("没有平台定价的模型也能保存，不再提示积分页签", () => {
    const { pricing: _p, ...rest } = base;
    expect(run(rest).some((item) => item.tab === "price")).toBe(false);
  });

  test("必填项没填是红点；选填项用默认值是黄点", () => {
    const { deadline: _drop, ...rest } = base;
    const checks = run({ ...rest, label: "" });
    expect(checks.map((item) => [item.path, item.level])).toEqual([
      ["label", "error"],
      ["deadline", "warning"],
    ]);
  });

  test("后端返回的历史定价问题在积分页签停用后不再展示", () => {
    const body = {
      ...base,
      pricing: { ...base.pricing, tiers: [{ on: true, when: { quality: "3k" }, unit: 5 }] },
    };
    const checks = run(body, [
      { path: "pricing.tiers[0].when", message: "条件不对" },
      { path: "pricing.unit", message: "价格不对" },
    ]);
    expect(checks.filter((item) => item.path?.startsWith("pricing."))).toEqual([]);
  });
});

describe("modelChecks：产品模型标识由后端生成", () => {
  test("新建时 key 为空不算问题", () => {
    const body = {
      key: "",
      label: "L",
      kind: "image",
      channels: [{ channel: "main", upstream_model: "u" }],
    };
    const channel = {
      key: "main",
      name: "主渠道",
      enabled: true,
      wallet_enabled: true,
      secret_set: true,
    } as ChannelView;
    const checks = modelChecks(body, resolveModelChannel(body, [channel], []), [], false);
    expect(checks.find((item) => item.path === "key")).toBeUndefined();
  });
});
