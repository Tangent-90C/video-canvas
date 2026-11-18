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
  /** TokenDance 内部模型 id（不透明，如 01KMM9…），**不是**模型名，别拿来匹配 */
  model_id: string;
  /** 模型别名（如 seedream-5.0-lite），与本站模型的 upstream_model 一致；上游没给时为空 */
  model_slug: string;
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
 * 钱包任务的「花了多少钱」只认这里的逐条真实花费。
 * 未启用 / 未连接返回空列表，上游失败返回旧缓存并标 stale。
 */
export const getWalletUsage = () => service.get<WalletUsage>("/wallet/tokenpay/usage");

/** TokenDance 充值会话状态：unknown 表示上游新增了前端尚未识别的状态，继续轮询 */
export type WalletRechargeStatus =
  | "pending"
  | "paid"
  | "failed"
  | "closed"
  | "refunded"
  | "unknown";

/** 后端充值会话响应（snake_case） */
export interface BackendWalletRecharge {
  /** 会话 ID */
  id: string;
  /** 充值金额（元，整数） */
  amount: number;
  /** 上游会话状态 */
  status: WalletRechargeStatus;
  /** 二维码内容；只有创建响应有 */
  payment_url?: string;
  /** 支付宝深链；上游可能不返回 */
  alipay_url?: string;
  /** 过期时间（RFC3339） */
  expires_at: string;
  /** 支付时间（RFC3339）；未支付为空 */
  paid_at?: string;
}

/** 前端使用的充值会话：时间转毫秒，缺失链接转 null */
export interface WalletRechargeSession {
  /** 会话 ID */
  id: string;
  /** 充值金额（元，整数） */
  amount: number;
  /** 会话状态 */
  status: WalletRechargeStatus;
  /** 二维码内容；查单响应沿用创建时的值 */
  paymentUrl: string | null;
  /** 支付宝深链；没有为 null */
  alipayUrl: string | null;
  /** 过期时间（毫秒时间戳） */
  expiresAt: number;
  /** 支付时间（毫秒时间戳）；未支付为 null */
  paidAt: number | null;
}

/**
 * 后端充值会话转前端结构。
 * @param dto 后端充值会话
 * @returns 前端充值会话
 */
export const mapWalletRecharge = (dto: BackendWalletRecharge): WalletRechargeSession => ({
  id: dto.id,
  amount: dto.amount,
  status: dto.status,
  paymentUrl: dto.payment_url || null,
  alipayUrl: dto.alipay_url || null,
  expiresAt: Date.parse(dto.expires_at),
  paidAt: dto.paid_at ? Date.parse(dto.paid_at) : null,
});

/**
 * 创建 TokenDance 充值会话；金额单位元，只接受整数。
 * @param amount 充值金额（元）
 * @returns 新建的充值会话
 */
export const createWalletRecharge = async (amount: number): Promise<WalletRechargeSession> =>
  mapWalletRecharge(
    await service.post<BackendWalletRecharge>("/wallet/tokenpay/recharge/sessions", { amount }),
  );

/**
 * 查询充值会话状态；轮询使用 silent，避免网络波动反复弹全局错误。
 * @param sessionId 充值会话 ID
 * @param silent 失败时不弹全局提示
 * @returns 最新充值会话
 */
export const getWalletRecharge = async (
  sessionId: string,
  silent = true,
): Promise<WalletRechargeSession> =>
  mapWalletRecharge(
    await service.get<BackendWalletRecharge>(
      `/wallet/tokenpay/recharge/sessions/${encodeURIComponent(sessionId)}`,
      undefined,
      { silent },
    ),
  );

/** 断开钱包：删除本站保存的 Key 与授权信息 */
export const disconnectTokenPay = () => service.delete<void>("/wallet/tokenpay/connection");
