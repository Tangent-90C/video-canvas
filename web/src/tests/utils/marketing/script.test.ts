import { describe, expect, test } from "bun:test";

import type { Round, Script } from "@/api/marketing/type";
import {
  failedTaskRef,
  methodLabel,
  playableTakes,
  roundAspect,
  flagSummary,
  flaggedShots,
  isScriptBusy,
  narrationsChanged,
  renderAllState,
  scriptRenderState,
  scriptStatusLabel,
} from "@/utils/marketing/script";

const script = (patch: Partial<Script> = {}): Script => ({
  id: "x1",
  roundId: "r1",
  kind: "create",
  angle: "pain",
  title: "t",
  hook: "",
  shots: [
    {
      start: 0,
      end: 3,
      purpose: "",
      shot: "特写",
      camera: "固定",
      scene: "s",
      narration: "第一句",
      refShot: null,
    },
    {
      start: 3,
      end: 6,
      purpose: "",
      shot: "中景",
      camera: "固定",
      scene: "s",
      narration: "第二句",
      refShot: null,
    },
  ],
  music: "",
  voice: "",
  flags: [],
  status: "draft",
  edited: false,
  method: null,
  takes: [],
  renderCredits: 100,
  error: "",
  revision: 1,
  ...patch,
});

const round = (scripts: Script[], patch: Partial<Round> = {}): Round =>
  ({ id: "r1", status: "ready", scripts, ...patch }) as Round;

describe("脚本出片按钮", () => {
  test("正常脚本可出片；已出片 / 失败的叫重出", () => {
    expect(scriptRenderState(script())).toEqual({ disabled: false, reason: "", again: false });
    expect(scriptRenderState(script({ status: "done" })).again).toBe(true);
    expect(scriptRenderState(script({ status: "failed" })).again).toBe(true);
  });

  test("出片中 / 重写中禁用", () => {
    expect(scriptRenderState(script({ status: "rendering" })).disabled).toBe(true);
    expect(scriptRenderState(script({ status: "rewriting" })).disabled).toBe(true);
    expect(isScriptBusy("rendering")).toBe(true);
    expect(isScriptBusy("rewriting")).toBe(true);
    expect(isScriptBusy("done")).toBe(false);
  });

  test("旁白有问题：禁用并列出问题", () => {
    const flagged = script({
      flags: [
        { shot: 0, type: "forbidden", text: "最" },
        { shot: 1, type: "claim", text: "销量第一" },
        { shot: 1, type: "forbidden", text: "最" },
      ],
    });
    const state = scriptRenderState(flagged);
    expect(state.disabled).toBe(true);
    expect(state.reason).toBe("旁白有问题：禁用说法「最」、背书无依据「销量第一」");
  });

  test("平台积分余额不参与出片判断", () => {
    expect(scriptRenderState(script()).disabled).toBe(false);
  });

  test("问题镜头集合与按类型去重的问题标签", () => {
    const flags = [
      { shot: 0, type: "forbidden" as const, text: "最" },
      { shot: 2, type: "brand" as const, text: "某品牌" },
      { shot: 0, type: "forbidden" as const, text: "第一" },
    ];
    expect([...flaggedShots(flags)].sort()).toEqual([0, 2]);
    expect(flagSummary(flags)).toEqual(["forbidden", "brand"]);
  });

  test("状态标签", () => {
    expect(scriptStatusLabel("draft")).toBe("待出片");
    expect(scriptStatusLabel("rewriting")).toBe("重写中");
    expect(scriptStatusLabel("rendering")).toBe("出片中");
    expect(scriptStatusLabel("done")).toBe("已出片");
    expect(scriptStatusLabel("failed")).toBe("出片失败");
  });
});

describe("旁白编辑", () => {
  test("只有真的改了才需要保存：修剪首尾空白后比较", () => {
    const s = script();
    expect(narrationsChanged(s, ["第一句", "第二句"])).toBe(false);
    expect(narrationsChanged(s, [" 第一句 ", "第二句"])).toBe(false);
    expect(narrationsChanged(s, ["第一句", "改了"])).toBe(true);
    expect(narrationsChanged(s, ["第一句"])).toBe(true);
  });
});

describe("全部出片", () => {
  test("只统计可出片的脚本：出片中、重写中、旁白有问题的不算", () => {
    const state = renderAllState(
      round([
        script({ id: "a", renderCredits: 100 }),
        script({ id: "b", renderCredits: 100, status: "done" }),
        script({ id: "c", status: "rendering" }),
        script({ id: "d", flags: [{ shot: 0, type: "brand", text: "x" }] }),
      ]),
    );
    expect(state).toEqual({ show: true, count: 2, disabled: false, reason: "" });
  });

  test("可出片的不足 2 份不显示按钮；轮次不是 ready 不显示；余额不参与判断", () => {
    expect(renderAllState(round([script()])).show).toBe(false);
    expect(
      renderAllState(round([script({ id: "a" }), script({ id: "b" })], { status: "scripting" }))
        .show,
    ).toBe(false);
    const ready = renderAllState(round([script({ id: "a" }), script({ id: "b" })]));
    expect(ready).toMatchObject({ show: true, disabled: false, reason: "" });
  });
});

describe("方法小字与画幅", () => {
  test("方法：内置 / 技能名 vN / 停用退回显示内置 / 没有方法也是内置", () => {
    const base = { skillTitle: "", skillName: "", version: 0, fallback: false };
    expect(methodLabel(null)).toBe("内置方法");
    expect(methodLabel({ ...base, source: "builtin" })).toBe("内置方法");
    expect(methodLabel({ ...base, source: "skill", skillName: "daihuo-fanpai", version: 2 })).toBe(
      "daihuo-fanpai v2",
    );
    expect(
      methodLabel({
        ...base,
        source: "skill",
        skillName: "daihuo-fanpai",
        version: 2,
        fallback: true,
      }),
    ).toBe("内置方法");
  });

  test("成片画幅取 aspect_ratio 或 ratio，不认识按 9:16", () => {
    expect(roundAspect({ params: { aspect_ratio: "16:9" } })).toEqual([16, 9]);
    expect(roundAspect({ params: { ratio: "3:4" } })).toEqual([3, 4]);
    expect(roundAspect({ params: { aspect_ratio: "auto" } })).toEqual([9, 16]);
    expect(roundAspect({ params: {} })).toEqual([9, 16]);
  });

  test("可播放的 take：只留有成片地址的", () => {
    const takes = [
      { id: "1", outputs: [] },
      { id: "2", outputs: [{ url: "u", posterUrl: "" }] },
    ] as unknown as Script["takes"];
    expect(playableTakes(script({ takes })).map((t) => t.id)).toEqual(["2"]);
  });

  test("出片失败时的任务编号取最近一条出片任务，其余状态没有", () => {
    const takes = [
      { id: "1", taskRef: "aa11" },
      { id: "2", taskRef: "bb22" },
    ] as unknown as Script["takes"];
    expect(failedTaskRef(script({ status: "failed", takes }))).toBe("bb22");
    expect(failedTaskRef(script({ status: "done", takes }))).toBeNull();
    expect(failedTaskRef(script({ status: "failed", takes: [] }))).toBeNull();
  });
});
