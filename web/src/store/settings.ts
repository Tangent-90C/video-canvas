import { create } from "zustand";
import { persist } from "zustand/middleware";

import { applyTheme, type ThemeSetting } from "@/lib/theme";

/** 背景网格样式，"none" 表示不画背景 */
export type CanvasBackground = "dots" | "lines" | "cross" | "none";

/** 滚轮的默认行为：平移画布，或直接缩放 */
export type CanvasWheelMode = "pan" | "zoom";

/** 存下来的那部分设置，不含下面那些操作 */
export type Settings = {
  // 画布
  background: CanvasBackground;
  wheelMode: CanvasWheelMode;
  // 通用
  theme: ThemeSetting;
  /** 空画布时顶部那条操作提示 */
  showHints: boolean;
  // 模型：种类 -> 模型 id，新建节点时按它预选，没记的种类走清单第一条
  defaultModels: Record<string, string>;
};

type SettingsActions = {
  updateSettings: <K extends keyof Settings>(key: K, value: Settings[K]) => void;
  /** 单独改某个种类的默认模型，其余种类原样留着 */
  setDefaultModel: (kind: string, modelId: string) => void;
  resetSettings: () => void;
};

export type SettingsStore = Settings & SettingsActions;

/** 背景点阵 / 线条的间距 */
export const GRID_SIZE = 20;

const DEFAULT_SETTINGS: Settings = {
  background: "dots",
  wheelMode: "pan",
  theme: "dark",
  showHints: true,
  defaultModels: {},
};

const STORAGE_KEY = "video-design-settings";

/**
 * 全局设置：画布要读，节点的模型下拉也要读，所以放一个 store 里。
 * persist 负责写进 localStorage，刷新后还在；存档坏了 zustand 会退回默认值。
 */
export const useSettingsStore = create<SettingsStore>()(
  persist(
    (set, get) => ({
      ...DEFAULT_SETTINGS,

      updateSettings: (key, value) => {
        set({ [key]: value } as Pick<Settings, typeof key>);
        if (key === "theme") applyTheme(get().theme);
      },

      setDefaultModel: (kind, modelId) =>
        set({ defaultModels: { ...get().defaultModels, [kind]: modelId } }),

      resetSettings: () => {
        set(DEFAULT_SETTINGS);
        applyTheme(DEFAULT_SETTINGS.theme);
      },
    }),
    {
      name: STORAGE_KEY,
      // 只存数据，操作函数不进存档
      partialize: ({ background, wheelMode, theme, showHints, defaultModels }): Settings => ({
        background,
        wheelMode,
        theme,
        showHints,
        defaultModels,
      }),
      // 读回存档后立刻贴上主题，跟着 initTheme 一起保证首屏不闪色
      onRehydrateStorage: () => (state) => {
        if (state) applyTheme(state.theme);
      },
    },
  ),
);

/**
 * 首屏套用存档里的主题。
 * 要在 React 挂载前调用，否则深色存档会先闪一下浅色。
 */
export function initTheme() {
  applyTheme(useSettingsStore.getState().theme);
}
