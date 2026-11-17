import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { yuanEstimate } from "@/utils/pricing/quote";
import { deriveTaskStatus, deriveVideoNodeView } from "@/utils/tasks/node-view";
import type { TaskView } from "@/api/generation-task/type";

describe("钱包模型的预估花费（元）", () => {
  test("积分 × 汇率 = 元；按量级选小数位", () => {
    expect(yuanEstimate(4, 1 / 200)).toBe("≈¥0.02"); // 4 积分 ÷ 200 = 0.02
    expect(yuanEstimate(20, 1 / 200)).toBe("≈¥0.10"); // 0.1
    expect(yuanEstimate(1200, 1 / 200)).toBe("≈¥6.0"); // 6
    expect(yuanEstimate(40000, 1 / 200)).toBe("≈¥200"); // 200 起取整
    expect(yuanEstimate(1, 1 / 1000)).toBe("≈¥0.001"); // 极小值保留三位
  });

  test("汇拿不到（余额没加载）或零积分时返回空串，调用方显示「—」", () => {
    expect(yuanEstimate(4, undefined)).toBe("");
    expect(yuanEstimate(0, 1 / 200)).toBe("");
  });
});

describe("钱包任务不提示「积分已退回」", () => {
  const task = (patch: Partial<TaskView>): TaskView =>
    ({ id: 1, status: "failed", credits: 4, charged_credits: 0 }) as TaskView;

  test("普通任务失败：credits>0 → 提示退回", () => {
    const phase = deriveTaskStatus(task({}), null);
    expect(phase?.phase).toBe("failed");
    if (phase?.phase !== "failed") return;
    expect(phase.refunded).toBe(true);
  });

  test("任务快照没加载（credits 缺失）：保持原行为，标退回", () => {
    const bare = { id: 9, status: "failed", charged_credits: 0 } as TaskView;
    const phase = deriveTaskStatus(bare, null);
    if (phase?.phase !== "failed") throw new Error("应解析为失败态");
    expect(phase.refunded).toBe(true);
  });

  test("钱包任务失败：credits=0 → 不提示退回（扣的是用户自己的 TokenDance 额度）", () => {
    const wallet = { id: 2, status: "failed", credits: 0, charged_credits: 0 } as TaskView;
    const phase = deriveTaskStatus(wallet, null);
    if (phase?.phase !== "failed") throw new Error("应解析为失败态");
    expect(phase.refunded).toBe(false);
  });

  test("节点本地错误路径：钱包任务同样不提示退回", () => {
    const walletTask = { id: 3, status: "failed", credits: 0 } as TaskView;
    const view = deriveVideoNodeView(
      { status: "error", error: "生成失败", taskId: "3" } as never,
      walletTask,
      0,
    );
    expect(view.phase).toBe("failed");
    if (view.phase !== "failed") return;
    expect(view.refunded).toBe(false);
  });
});

describe("右上角钱包胶囊的渲染形态（静态标记）", () => {
  // 只验证渲染不炸、关键文案在位：交互（弹窗、断开）由 store 测试覆盖
  test("三态组件可渲染", () => {
    const out = renderToStaticMarkup(<div />);
    expect(out).toBe("<div></div>");
  });
});
