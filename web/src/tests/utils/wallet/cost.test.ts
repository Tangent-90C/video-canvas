import { describe, expect, test } from "bun:test";

import type { RecordDto } from "@/api/conversation/type";
import type { TaskView } from "@/api/generation-task/type";
import type { WalletUsageItem } from "@/api/wallet";
import { estimateCost, formatYuan, matchRecordCost } from "@/utils/wallet/cost";

/** 一条调用记录；时间用 UTC（上游 created_at 就是 UTC） */
/**
 * 一条调用记录。真实上游的 model_id 是不透明内部 id（如 01KMM9…），模型名在 model_slug 里——
 * 线上实测踩过：按 model_id 匹配永远匹配不上，界面全显示「—」。
 */
const usage = (model: string, at: string, cost: number, success = true): WalletUsageItem => ({
  model_id: `internal-${model}`,
  model_slug: model,
  created_at: at,
  success,
  cost_yuan: cost,
});

/** 一格任务：提交时间 + 截止时间 */
const task = (id: number, submittedAt: string, finishedAt?: string): TaskView =>
  ({
    id,
    status: "succeeded",
    credits: 0,
    charged_credits: 0,
    submitted_at: submittedAt,
    finished_at: finishedAt ?? null,
    deadline_at: null,
    created_at: submittedAt,
  }) as TaskView;

/** 一条记录：count 个格子，提交时间依次错开 */
const record = (count: number, submittedAt: string[]): RecordDto =>
  ({
    id: "r1",
    conversationId: "c1",
    kind: "image",
    modelId: "m",
    prompt: "p",
    input: {},
    count,
    tasks: submittedAt.map((_, index) => ({ id: index + 1 }) as TaskView),
    submitErrors: [],
    quoteCredits: 0,
    createdAt: submittedAt[0],
  }) as RecordDto;

const SEEDREAM = "seedream-5.0-lite";

describe("matchRecordCost：把 TokenDance 调用记录对到本站记录上", () => {
  test("单格：同模型、时间落在这一格窗口内，取真实花费", () => {
    // 12:00:00 提交，12:00:07 上游返回——TokenDance 门户里这条记 ¥0.22
    const items = [usage(SEEDREAM, "2026-11-17T12:00:07Z", 0.22)];
    const cost = matchRecordCost(
      record(1, ["2026-11-17T12:00:00Z"]),
      [task(1, "2026-11-17T12:00:00Z", "2026-11-17T12:00:09Z")],
      items,
      SEEDREAM,
    );
    expect(cost).toBe(0.22);
  });

  test("多格：每格各挑一条，不重复占用（4 张图就是 4 × 0.22）", () => {
    const at = (s: number) => `2026-11-17T12:0${s}:00Z`;
    const items = [
      usage(SEEDREAM, at(4), 0.22),
      usage(SEEDREAM, at(3), 0.22),
      usage(SEEDREAM, at(2), 0.22),
      usage(SEEDREAM, at(1), 0.22),
    ];
    const tasks = [1, 2, 3, 4].map((n) => task(n, at(n - 1), at(n)));
    const cost = matchRecordCost(record(4, [at(0), at(1), at(2), at(3)]), tasks, items, SEEDREAM);
    expect(cost).toBeCloseTo(0.88, 10);
  });

  test("按 model_slug 匹配，不认不透明的 model_id（线上实测：内部 id 与站内模型名无关）", () => {
    const items = [usage(SEEDREAM, "2026-11-17T12:00:04Z", 0.22)];
    const cost = matchRecordCost(
      record(1, ["2026-11-17T12:00:00Z"]),
      [task(1, "2026-11-17T12:00:00Z", "2026-11-17T12:00:05Z")],
      items,
      SEEDREAM,
    );
    expect(cost).toBe(0.22);
  });

  test("上游没给 model_slug 时退回 model_id", () => {
    const bare: WalletUsageItem = {
      model_id: SEEDREAM,
      model_slug: "",
      created_at: "2026-11-17T12:00:04Z",
      success: true,
      cost_yuan: 0.22,
    };
    const cost = matchRecordCost(
      record(1, ["2026-11-17T12:00:00Z"]),
      [task(1, "2026-11-17T12:00:00Z", "2026-11-17T12:00:05Z")],
      [bare],
      SEEDREAM,
    );
    expect(cost).toBe(0.22);
  });

  test("别的模型的记录不掺和（同时间也不顶替）", () => {
    const items = [
      usage("minimax-speech-2.8-hd", "2026-11-17T12:00:03Z", 0.05),
      usage(SEEDREAM, "2026-11-17T12:00:05Z", 0.22),
    ];
    const cost = matchRecordCost(
      record(1, ["2026-11-17T12:00:00Z"]),
      [task(1, "2026-11-17T12:00:00Z", "2026-11-17T12:00:06Z")],
      items,
      SEEDREAM,
    );
    expect(cost).toBe(0.22);
  });

  test("失败的调用也计入（它的花费本来就是 0）", () => {
    const items = [usage(SEEDREAM, "2026-11-17T12:00:04Z", 0, false)];
    const cost = matchRecordCost(
      record(1, ["2026-11-17T12:00:00Z"]),
      [task(1, "2026-11-17T12:00:00Z", "2026-11-17T12:00:05Z")],
      items,
      SEEDREAM,
    );
    expect(cost).toBe(0);
  });

  test("窗口外的记录不匹配：返回 null，界面显示「—」而不是猜一个数", () => {
    // 记录是三天前的，调用记录只拉了最近 30 天里的一页但没覆盖到它
    const items = [usage(SEEDREAM, "2026-11-14T12:00:04Z", 0.22)];
    const cost = matchRecordCost(
      record(1, ["2026-11-17T12:00:00Z"]),
      [task(1, "2026-11-17T12:00:00Z", "2026-11-17T12:00:05Z")],
      items,
      SEEDREAM,
    );
    expect(cost).toBeNull();
  });

  test("一格格不上就不给总额：半个金额比没有更容易误导", () => {
    const at = (s: number) => `2026-11-17T12:0${s}:00Z`;
    // 两格只有一格能对上
    const items = [usage(SEEDREAM, at(1), 0.22)];
    const cost = matchRecordCost(
      record(2, [at(0), at(5)]),
      [task(1, at(0), at(1)), task(2, at(5), at(6))],
      items,
      SEEDREAM,
    );
    expect(cost).toBeNull();
  });

  test("提交失败的格子没有任务，本来就没花钱，不参与匹配", () => {
    const items = [usage(SEEDREAM, "2026-11-17T12:00:04Z", 0.22)];
    const rec = {
      ...record(2, ["2026-11-17T12:00:00Z", "2026-11-17T12:00:00Z"]),
      // 第一格提交失败（tasks[0] 为 null），只有第二格有任务
      tasks: [null, { id: 2 } as TaskView],
    } as RecordDto;
    const cost = matchRecordCost(
      rec,
      [task(2, "2026-11-17T12:00:00Z", "2026-11-17T12:00:05Z")],
      items,
      SEEDREAM,
    );
    expect(cost).toBe(0.22);
  });

  test("上游模型名拿不到（模型已下线等）：直接放弃，不按时间瞎猜", () => {
    const items = [usage(SEEDREAM, "2026-11-17T12:00:04Z", 0.22)];
    const cost = matchRecordCost(
      record(1, ["2026-11-17T12:00:00Z"]),
      [task(1, "2026-11-17T12:00:00Z", "2026-11-17T12:00:05Z")],
      items,
      "",
    );
    expect(cost).toBeNull();
  });

  test("调用记录还没拉到（未连接 / 上游挂）：null", () => {
    expect(matchRecordCost(record(1, ["2026-11-17T12:00:00Z"]), [], [], SEEDREAM)).toBeNull();
  });
});

describe("estimateCost：下单前的预估（同模型最近真实花费的均价）", () => {
  const items = [
    usage(SEEDREAM, "2026-11-17T20:15:40Z", 0.22),
    usage(SEEDREAM, "2026-11-17T18:08:10Z", 0.22),
    usage(SEEDREAM, "2026-11-17T17:21:17Z", 0, false), // 失败的不算进均价
  ];

  test("均价 × 生成数量", () => {
    expect(estimateCost(items, SEEDREAM, 1)).toBeCloseTo(0.22, 10);
    expect(estimateCost(items, SEEDREAM, 4)).toBeCloseTo(0.88, 10);
  });

  test("没有同模型的历史记录：null（界面显示「—」，不拿别的模型顶数）", () => {
    expect(estimateCost(items, "seedream-6.0", 1)).toBeNull();
  });

  test("只有失败记录：null（花费 0 不能当单价）", () => {
    expect(
      estimateCost([usage(SEEDREAM, "2026-11-17T17:21:17Z", 0, false)], SEEDREAM, 1),
    ).toBeNull();
  });
});

describe("formatYuan：金额文案", () => {
  test("按量级选小数位", () => {
    expect(formatYuan(0.22)).toBe("¥0.22");
    expect(formatYuan(6)).toBe("¥6.0");
    expect(formatYuan(200)).toBe("¥200");
  });

  test("零与负数显示 ¥0", () => {
    expect(formatYuan(0)).toBe("¥0");
    expect(formatYuan(-1)).toBe("¥0");
  });
});
