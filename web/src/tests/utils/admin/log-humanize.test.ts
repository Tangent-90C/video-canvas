import { describe, expect, test } from "bun:test";

import {
  agoText,
  durationCell,
  kindTagView,
  statusDetail,
  auditActorRole,
  auditModeLabel,
  auditSourceTable,
  formatFullTime,
  auditCategory,
  drawerSections,
  failureAdvice,
  humanDuration,
  moreFilterCount,
  overviewCards,
  segmentValue,
  taskHeadline,
  taskSubline,
  taskVerdict,
  DEFAULT_USAGE_FILTERS,
  EVENT_TEXT,
  NO_TIMELINE_TEXT,
} from "@/utils/admin/log-rules";

const NOW = Date.parse("2026-10-18T06:30:00.000Z");
const base = {
  kind: "video" as const,
  status: "succeeded",
  is_test: false,
  error_message: "",
  model_key: "kling-v2",
  model_name: "可灵 v2",
  duration_ms: 42_000 as number | null,
  created_at: "2026-10-18T06:29:00.000Z",
};

describe("时长人话", () => {
  test("不足一分钟写秒，分钟带秒，整分钟省略秒，超过一小时写小时和分", () => {
    expect(humanDuration(42_000)).toBe("42 秒");
    expect(humanDuration(200_000)).toBe("3 分 20 秒");
    expect(humanDuration(180_000)).toBe("3 分钟");
    expect(humanDuration(3_900_000)).toBe("1 小时 5 分");
    expect(humanDuration(3_600_000)).toBe("1 小时");
  });
  test("不足 1 秒按 1 秒；空值为空串", () => {
    expect(humanDuration(300)).toBe("1 秒");
    expect(humanDuration(null)).toBe("");
  });
});

describe("任务一句话", () => {
  test("按类型和状态拼", () => {
    expect(taskHeadline({ kind: "video", status: "succeeded" })).toBe("生成了一个视频");
    expect(taskHeadline({ kind: "image", status: "failed" })).toBe("生成图片失败了");
    expect(taskHeadline({ kind: "video", status: "expired" })).toBe("生成视频超时了");
    expect(taskHeadline({ kind: "audio", status: "canceled" })).toBe("取消了音频生成");
    expect(taskHeadline({ kind: "video", status: "pending" })).toBe("正在排队生成视频");
    expect(taskHeadline({ kind: "video", status: "queued" })).toBe("正在排队生成视频");
    expect(taskHeadline({ kind: "text", status: "running" })).toBe("正在生成文本");
    expect(taskHeadline({ kind: "text", status: "finalizing" })).toBe("正在生成文本");
  });
});

describe("任务第二行", () => {
  test("成功：模型 · 用时", () => {
    expect(taskSubline(base, NOW)).toBe("可灵 v2 · 用时 42 秒");
  });
  test("模型展示名缺失回落 key", () => {
    expect(taskSubline({ ...base, model_name: "" }, NOW)).toContain("kling-v2");
  });
  test("失败：有用户文案就写原因，没有文案只写模型", () => {
    expect(taskSubline({ ...base, status: "failed", error_message: "平台暂时繁忙" }, NOW)).toBe(
      "可灵 v2 · 平台暂时繁忙",
    );
    expect(taskSubline({ ...base, status: "failed" }, NOW)).toBe("可灵 v2");
  });
  test("超时与取消", () => {
    expect(taskSubline({ ...base, status: "expired" }, NOW)).toBe("可灵 v2 · 等太久没出结果");
    expect(taskSubline({ ...base, status: "canceled" }, NOW)).toBe("可灵 v2");
  });
  test("进行中：排队中 / 生成中 + 已等多久，用传入的当前时间算", () => {
    const active = { ...base, duration_ms: null };
    expect(taskSubline({ ...active, status: "pending" }, NOW)).toBe(
      "可灵 v2 · 排队中 · 已等 1 分钟",
    );
    expect(taskSubline({ ...active, status: "running" }, NOW)).toBe("可灵 v2 · 生成中 · 已 1 分钟");
  });
  test("试跑：任务展示不涉及平台积分", () => {
    expect(taskSubline({ ...base, is_test: true }, NOW)).toBe("可灵 v2 · 用时 42 秒");
  });
});

describe("结论卡", () => {
  test("成功", () => {
    expect(taskVerdict(base, NOW)).toEqual({
      tone: "success",
      title: "生成成功",
      sub: "用时 42 秒",
      pulse: false,
    });
  });
  test("失败带用户文案；没有文案写生成失败", () => {
    expect(taskVerdict({ ...base, status: "failed", error_message: "繁忙" }, NOW)).toMatchObject({
      tone: "danger",
      title: "生成失败",
      sub: "繁忙",
    });
    expect(taskVerdict({ ...base, status: "failed" }, NOW).sub).toBe("生成失败");
  });
  test("超时、取消、进行中", () => {
    expect(taskVerdict({ ...base, status: "expired" }, NOW)).toMatchObject({
      tone: "warning",
      title: "等太久，已超时",
    });
    expect(taskVerdict({ ...base, status: "canceled" }, NOW)).toMatchObject({
      tone: "neutral",
      title: "已取消",
    });
    const running = taskVerdict({ ...base, status: "running" }, NOW);
    expect(running).toMatchObject({ tone: "info", title: "正在生成", pulse: true });
    expect(running.sub).toBe("已 1 分钟");
    expect(taskVerdict({ ...base, status: "queued" }, NOW).title).toBe("排队中");
  });
});

describe("失败时怎么办", () => {
  test("按错误码给提示", () => {
    expect(failureAdvice("moderation", "failed")?.text).toContain("没通过审核");
    expect(failureAdvice("invalid_param", "failed")?.title).toBe("不是系统问题");
    expect(failureAdvice("provider_balance", "failed")?.text).toContain("运维");
    expect(failureAdvice("submit_unknown", "failed")?.title).toBe("建议核对");
    expect(failureAdvice("timeout", "failed")?.title).toBe("等太久，已超时");
  });
  test("按状态：超时、取消", () => {
    expect(failureAdvice("", "expired")?.title).toBe("等太久，已超时");
    expect(failureAdvice("", "canceled")?.title).toBe("已取消");
  });
  test("provider_error 和未知错误码给通用文案", () => {
    const a = failureAdvice("provider_error", "failed");
    expect(a?.title).toBe("可以先让用户重试");
    expect(failureAdvice("weird_code", "failed")).toEqual(a);
    expect(failureAdvice("", "failed")).toEqual(a);
  });
  test("成功和进行中没有提示", () => {
    expect(failureAdvice("", "succeeded")).toBeNull();
    expect(failureAdvice("", "running")).toBeNull();
  });
});

describe("事件人话", () => {
  test("七种事件", () => {
    expect(EVENT_TEXT).toEqual({
      created: "提交了任务",
      submitted: "平台开始处理",
      finalizing: "生成完成，正在保存结果",
      succeeded: "完成",
      failed: "失败了",
      canceled: "被取消",
      expired: "等太久，已超时",
    });
  });
  test("老任务提示", () => {
    expect(NO_TIMELINE_TEXT).toBe("无记录：此任务创建于日志功能上线前，没有状态变化记录。");
  });
});

describe("抽屉区块可见性", () => {
  test("使用记录抽屉不再分简单 / 详细：全部区块都显示，试跑追踪只在有追踪时", () => {
    const s = drawerSections({ hasTrace: false });
    expect(Object.entries(s).filter(([k, v]) => k !== "trace" && !v)).toEqual([]);
    expect(s.trace).toBe(false);
    expect(drawerSections({ hasTrace: true }).trace).toBe(true);
  });
});

describe("概览卡", () => {
  const summary = {
    total: 128,
    succeeded: 112,
    failed: 8,
    canceled: 1,
    expired: 2,
    active: 5,
  };
  test("需要关注 = 失败 + 超时 + 取消；成功率 = 成功 ÷ (全部 − 进行中)", () => {
    const o = overviewCards(summary);
    expect(o.total).toBe(128);
    expect(o.succeeded).toBe(112);
    expect(o.attention).toEqual({ count: 11, failed: 8, expired: 2, canceled: 1 });
    expect(o.active).toBe(5);
    expect(o.successRate).toBe("91.1%");
  });
  test("成功率保留一位小数；分母为 0 显示 0%", () => {
    expect(overviewCards({ ...summary, total: 120, succeeded: 105, active: 0 }).successRate).toBe(
      "87.5%",
    );
    expect(
      overviewCards({
        ...summary,
        total: 5,
        succeeded: 0,
        failed: 0,
        canceled: 0,
        expired: 0,
        active: 5,
      }).successRate,
    ).toBe("0%");
  });
});

describe("状态分段与更多筛选", () => {
  test("取消、超时不在分段里，此时没有选中项", () => {
    expect(segmentValue("all")).toBe("all");
    expect(segmentValue("succeeded")).toBe("succeeded");
    expect(segmentValue("failed")).toBe("failed");
    expect(segmentValue("active")).toBe("active");
    expect(segmentValue("canceled")).toBeNull();
    expect(segmentValue("expired")).toBeNull();
  });
  test("更多筛选的已选数量：类型、模型、含试跑", () => {
    expect(moreFilterCount(DEFAULT_USAGE_FILTERS)).toBe(0);
    expect(
      moreFilterCount({ ...DEFAULT_USAGE_FILTERS, kind: "video", model: "m", includeTest: true }),
    ).toBe(3);
    expect(moreFilterCount({ ...DEFAULT_USAGE_FILTERS, userId: 3, status: "failed" })).toBe(0);
  });
});

describe("审计分类", () => {
  test("取动作前缀；不认识的归到其它", () => {
    expect(auditCategory("user.ban")).toBe("user");
    expect(auditCategory("channel.auto_off")).toBe("channel");
    expect(auditCategory("payment.order_close")).toBe("payment");
    expect(auditCategory("plugin.upgrade_models")).toBe("plugin");
    expect(auditCategory("foo.bar")).toBe("other");
    expect(auditCategory("")).toBe("other");
  });
});

describe("相对时间", () => {
  const now = Date.parse("2026-10-18T06:30:00.000Z");
  const at = (minutes: number) => new Date(now - minutes * 60_000).toISOString();
  test("刚刚 / 分钟 / 小时 / 天前", () => {
    expect(agoText(at(0.2), now)).toBe("刚刚");
    expect(agoText(at(3), now)).toBe("3 分钟前");
    expect(agoText(at(150), now)).toBe("2 小时前");
    expect(agoText(at(60 * 24 * 3), now)).toBe("3 天前");
  });
  test("超过 30 天写日期；空值和坏值原样处理", () => {
    expect(agoText(at(60 * 24 * 45), now)).toMatch(/^\d{4}-\d\d-\d\d \d\d:\d\d$/);
    expect(agoText("", now)).toBe("—");
    expect(agoText("bad", now)).toBe("bad");
  });
});

describe("审计表格与抽屉的展示逻辑", () => {
  test("操作者第二行：后台管理员 / 系统自动", () => {
    expect(auditActorRole(3)).toBe("后台管理员");
    expect(auditActorRole(0)).toBe("系统自动");
  });
  test("自动 / 手动标签", () => {
    expect(auditModeLabel(0)).toBe("自动");
    expect(auditModeLabel(7)).toBe("手动");
  });
  test("来源表", () => {
    expect(auditSourceTable("admin")).toBe("admin_audit_logs");
    expect(auditSourceTable("ai")).toBe("ai_audit_logs");
  });
  test("完整时间 YYYY/MM/DD HH:mm:ss；空值和坏值", () => {
    expect(formatFullTime(new Date(2026, 9, 18, 14, 5, 9).toISOString())).toBe(
      "2026/10/18 14:05:09",
    );
    expect(formatFullTime(null)).toBe("—");
    expect(formatFullTime("bad")).toBe("bad");
  });
});

describe("使用记录表格单元格", () => {
  test("类型标签：每种类型一种 tone 和图标", () => {
    expect(kindTagView("video")).toEqual({ label: "视频", tone: "info", icon: "video" });
    expect(kindTagView("image")).toEqual({ label: "图片", tone: "violet", icon: "image" });
    expect(kindTagView("audio")).toEqual({ label: "音频", tone: "orange", icon: "audio" });
    expect(kindTagView("text")).toEqual({ label: "文本", tone: "neutral", icon: "text" });
    expect(kindTagView("weird")).toEqual({ label: "weird", tone: "neutral", icon: "other" });
  });

  test("状态第二行：失败写用户文案，超时写原因，其余留空", () => {
    expect(statusDetail({ status: "failed", error_message: "平台繁忙" })).toBe("平台繁忙");
    expect(statusDetail({ status: "failed", error_message: "" })).toBe("");
    expect(statusDetail({ status: "expired", error_message: "" })).toBe("等太久没出结果");
    expect(statusDetail({ status: "canceled", error_message: "" })).toBe("");
    expect(statusDetail({ status: "succeeded", error_message: "" })).toBe("");
    expect(statusDetail({ status: "running", error_message: "" })).toBe("");
  });

  test("耗时格：完成的写时长，进行中写已多久，没有写破折号", () => {
    const now = Date.parse("2026-10-18T06:30:00.000Z");
    const base = {
      status: "succeeded",
      duration_ms: 42_000 as number | null,
      created_at: "2026-10-18T06:29:00.000Z",
    };
    expect(durationCell(base, now)).toBe("42 秒");
    expect(durationCell({ ...base, status: "running", duration_ms: null }, now)).toBe("已 1 分钟");
    expect(durationCell({ ...base, duration_ms: null }, now)).toBe("—");
  });
});
