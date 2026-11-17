import { describe, expect, test } from "bun:test";

import { saveCarry, saveComposerDraft, takeCarry, takeComposerDraft } from "@/utils/home/carry";

/** 每个用例一套干净的 sessionStorage，互不污染 */
function freshStorage() {
  const map = new Map<string, string>();
  (globalThis as { sessionStorage?: unknown }).sessionStorage = {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

describe("输入卡片草稿暂存（钱包授权整页跳转用）", () => {
  test("存入后按来源页取出；来源不匹配直接丢弃", () => {
    freshStorage();
    saveComposerDraft({ text: "一只猫", refs: [], from: "/" });
    expect(takeComposerDraft("/")?.text).toBe("一只猫");
    // 取过一次就没了（一次性）
    expect(takeComposerDraft("/")).toBeNull();

    saveComposerDraft({ text: "别处的草稿", refs: [], from: "/canvas/abc" });
    // 在首页取画布的草稿：不匹配，丢弃而不是留着错填
    expect(takeComposerDraft("/")).toBeNull();
    expect(takeComposerDraft("/canvas/abc")).toBeNull();
  });

  test("参考图只带已上传的，形状完整可恢复", () => {
    freshStorage();
    saveComposerDraft({
      text: "p",
      refs: [{ kind: "image", id: "r1", assetId: "12", url: "/files/12", name: "a.png" }],
      from: "/conversations/c1",
    });
    const draft = takeComposerDraft("/conversations/c1");
    expect(draft?.refs).toEqual([
      { kind: "image", id: "r1", assetId: "12", url: "/files/12", name: "a.png" },
    ]);
  });

  test("空白文本不记（和做同款一致）", () => {
    freshStorage();
    saveCarry("   ");
    expect(takeCarry()).toBe("");
  });

  test("sessionStorage 不可用时不炸，只是不带入", () => {
    (globalThis as { sessionStorage?: unknown }).sessionStorage = {
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => {
        throw new Error("blocked");
      },
      removeItem: () => {
        throw new Error("blocked");
      },
    };
    expect(() => saveComposerDraft({ text: "x", refs: [], from: "/" })).not.toThrow();
    expect(takeComposerDraft("/")).toBeNull();
  });
});
