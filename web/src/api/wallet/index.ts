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

/** 断开钱包：删除本站保存的 Key 与授权信息 */
export const disconnectTokenPay = () => service.delete<void>("/wallet/tokenpay/connection");
