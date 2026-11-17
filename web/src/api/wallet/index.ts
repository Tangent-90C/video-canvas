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

/** 钱包余额视图：TokenDance 账户余额与已消耗（元），以及「积分折元」汇率 */
export interface WalletBalance {
  /** 本站是否启用了钱包连接 */
  enabled: boolean;
  /** 当前用户是否已连接 */
  connected: boolean;
  /** 账户剩余额度（元） */
  balance_yuan: number;
  /** 账户已消耗（元） */
  used_yuan: number;
  /** 1 平台积分折多少元（钱包模型的预估花费用） */
  yuan_per_credit: number;
  /** 降级数据（上游查询失败 / Key 读不到）：可能不是最新 */
  stale: boolean;
}

/**
 * 查询钱包余额（Bearer 是用户自己的 TokenDance Key，后端代理 + 60 秒缓存）。
 * 未启用 / 未连接返回降级视图，不报错。
 */
export const getWalletBalance = () => service.get<WalletBalance>("/wallet/tokenpay/balance");

/** 断开钱包：删除本站保存的 Key 与授权信息 */
export const disconnectTokenPay = () => service.delete<void>("/wallet/tokenpay/connection");
