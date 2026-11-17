/** 「做同款」要带进首页输入框的提示词存在 sessionStorage 里的键 */
const CARRY_KEY = "video-canvas:carry-prompt";

/** 钱包授权跳转前暂存的输入卡片草稿（授权是整页跳转，SPA 状态会全丢） */
const DRAFT_KEY = "video-canvas:composer-draft";

/**
 * 记下登录后要填进首页输入框的提示词。放 sessionStorage 而不是 URL，
 * 免得带进浏览器历史和服务端日志；存不进去（隐私模式等）就只是不带入，不影响登录。
 * @param text 提示词；空白不记
 */
export const saveCarry = (text: string) => {
  const value = text.trim();
  if (!value) return;
  try {
    sessionStorage.setItem(CARRY_KEY, value);
  } catch {
    /** 存不进去只是不带入 */
  }
};

/**
 * 取出并清掉带入的提示词：只会被取到一次
 * @returns 提示词；没有或读不了为空串
 */
export const takeCarry = () => {
  try {
    const value = sessionStorage.getItem(CARRY_KEY) ?? "";
    sessionStorage.removeItem(CARRY_KEY);
    return value;
  } catch {
    return "";
  }
};

/** 暂存的输入卡片草稿：文本 + 已上传的参考图 + 来源页（授权完成后只回来源页恢复） */
export type ComposerDraft = {
  text: string;
  refs: ComposerDraftRef[];
  /** 草稿所在页面（授权跳转前的地址），恢复时按它匹配，防止在别的页面被错填 */
  from: string;
};

/** 草稿里的参考图：只带已上传完成的（上传中的恢复不了，回来重新加） */
export type ComposerDraftRef = {
  kind: string;
  id: string;
  assetId?: string;
  url?: string;
  name: string;
};

/**
 * 暂存输入卡片草稿（钱包授权跳转前调用）。放 sessionStorage：授权回来这一跳还有效，
 * 关掉页面就丢，不会在下次访问时突然冒出旧内容。
 */
export const saveComposerDraft = (draft: ComposerDraft) => {
  try {
    sessionStorage.setItem(DRAFT_KEY, JSON.stringify(draft));
  } catch {
    /** 存不进去只是不带入 */
  }
};

/**
 * 取出草稿并清除。来源页和当前页不一致时直接丢弃：那说明草稿属于别的页面，
 * 留着会在下次进那个页面时突然冒出旧内容。
 * @param from 当前页面地址
 * @returns 草稿；没有、读不了或来源不匹配为 null
 */
export const takeComposerDraft = (from: string): ComposerDraft | null => {
  try {
    const raw = sessionStorage.getItem(DRAFT_KEY);
    sessionStorage.removeItem(DRAFT_KEY);
    if (!raw) return null;
    const draft = JSON.parse(raw) as ComposerDraft;
    if (draft.from !== from) return null;
    return draft;
  } catch {
    return null;
  }
};
