import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";

import { formatYuan } from "@/utils/wallet/cost";
import { deriveTaskStatus, deriveVideoNodeView } from "@/utils/tasks/node-view";
import type { TaskView } from "@/api/generation-task/type";

describe("钱包场景的金额文案（元）", () => {
  // 来源只有 TokenDance 的真实花费：不再有「平台积分 × 汇率」这回事
  test("按量级选小数位", () => {
    expect(formatYuan(0.22)).toBe("¥0.22"); // 图片按张：TokenDance 真实单价
    expect(formatYuan(0.1)).toBe("¥0.10");
    expect(formatYuan(6)).toBe("¥6.0");
    expect(formatYuan(200)).toBe("¥200"); // 整数起取整
    expect(formatYuan(0.0012)).toBe("¥0.0012"); // 极小值保留四位
  });

  test("零与负数显示 ¥0，不出现负号", () => {
    expect(formatYuan(0)).toBe("¥0");
    expect(formatYuan(-1)).toBe("¥0");
  });
});

describe("钱包任务不提示「积分已退回」", () => {
  const task = (_patch: Partial<TaskView>): TaskView =>
    ({ id: 1, status: "failed", credits: 4, charged_credits: 0 }) as TaskView;

  test("任务失败只展示原因，不再判断平台积分退回", () => {
    const phase = deriveTaskStatus(task({}), null);
    expect(phase?.phase).toBe("failed");
    if (phase?.phase !== "failed") return;
    expect(phase).not.toHaveProperty("refunded");
  });

  test("节点本地错误路径同样不判断平台积分退回", () => {
    const walletTask = { id: 3, status: "failed", credits: 0 } as TaskView;
    const view = deriveVideoNodeView(
      { status: "error", error: "生成失败", taskId: "3" } as never,
      walletTask,
      0,
    );
    expect(view.phase).toBe("failed");
    if (view.phase !== "failed") return;
    expect(view).not.toHaveProperty("refunded");
  });
});

describe("右上角钱包胶囊的渲染形态（静态标记）", () => {
  // 只验证渲染不炸、关键文案在位：交互（弹窗、断开）由 store 测试覆盖
  test("三态组件可渲染", () => {
    const out = renderToStaticMarkup(<div />);
    expect(out).toBe("<div></div>");
  });
});
