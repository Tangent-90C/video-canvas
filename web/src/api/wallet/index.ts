import service from "@/utils/requests/service";

/** 用户钱包的展示视图；Key 只回掩码 */
export interface WalletView {
  /** 本站是否启用了钱包连接（部署时配置 tokenpay.app_url） */
  enabled: boolean;
  /** 当前用户是否已连接 */
  connected: boolean;
  /** 授权时登记的站点地址（归因 App URL） */
  app_url: string;
  /** Key 掩码，如 tk-ab12…ef34 */
  key_masked: string;
  /** 最近一次连接时间（RFC3339） */
  connected_at: string;
}

/** 发起授权的响应：前端整页跳转到 auth_url，TokenDance 确认后 302 回本站回调页 */
export interface TokenPayAuthStart {
  auth_url: string;
}

/**
 * 发起 TokenPay 授权；next 是授权完成回来后要去的页面（如 /profile?tab=wallet）
 */
export const startTokenPayAuth = (next: string) =>
  service.get<TokenPayAuthStart>("/wallet/tokenpay/start", next ? { next } : undefined);

/**
 * 用回调带回的一次性授权码交换并保存 Key；Key 只保存，任何接口都读不回明文
 */
export const connectTokenPay = (flow: string, code: string) =>
  service.post<WalletView>("/wallet/tokenpay/connect", { flow, code });

/** 查询当前钱包状态 */
export const getWalletConnection = () => service.get<WalletView>("/wallet/tokenpay/connection");

/** 钱包余额视图：TokenDance 账户余额与已消耗（元） */
export interface WalletBalance {
  /** 本站是否启用了钱包连接 */
  enabled: boolean;
  /** 当前用户是否已连接 */
  connected: boolean;
  /** 账户剩余额度（元） */
  balance_yuan: number;
  /** 账户已消耗（元） */
  used_yuan: number;
  /** 降级数据（上游查询失败 / Key 读不到）：可能不是最新 */
  stale: boolean;
}

/**
 * 查询钱包余额（Bearer 是用户自己的 TokenDance Key，后端代理 + 60 秒缓存）。
 * 未启用 / 未连接返回降级视图，不报错。
 */
export const getWalletBalance = () => service.get<WalletBalance>("/wallet/tokenpay/balance");

/** 一条上游调用记录：金额单位元，created_at 是 UTC RFC3339 */
export interface WalletUsageItem {
  /** TokenDance 内部模型 id，与本站模型的 upstream_model 一致 */
  model_id: string;
  created_at: string;
  /** 是否成功 */
  success: boolean;
  /** 真实花费（元） */
  cost_yuan: number;
}

/** 调用记录视图：钱包界面所有金额的唯一来源 */
export interface WalletUsage {
  /** 本站是否启用了钱包连接 */
  enabled: boolean;
  /** 当前用户是否已连接 */
  connected: boolean;
  /** 调用记录，newest first */
  items: WalletUsageItem[];
  /** 降级数据（上游查询失败）：展示的是旧缓存 */
  stale: boolean;
}

/**
 * 查询调用记录（Bearer 是用户自己的 TokenDance Key，后端代理 + 60 秒缓存）。
 * 钱包任务不扣平台积分，「花了多少钱」只认这里的逐条真实花费。
 * 未启用 / 未连接返回空列表，上游失败返回旧缓存并标 stale。
 */
export const getWalletUsage = () => service.get<WalletUsage>("/wallet/tokenpay/usage");

/** 断开钱包：删除本站保存的 Key 与授权信息 */
export const disconnectTokenPay = () => service.delete<void>("/wallet/tokenpay/connection");
