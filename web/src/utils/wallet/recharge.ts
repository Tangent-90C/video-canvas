import type { TaskView } from "@/api/generation-task/type";
import type { WalletRechargeStatus } from "@/api/wallet";

/** 常用充值金额（元） */
export const RECHARGE_PRESETS = [10, 30, 50, 100] as const;

/** 默认充值金额（元） */
export const DEFAULT_WALLET_RECHARGE_AMOUNT = 30;

/** 单次充值金额上下限（元） */
export const MIN_WALLET_RECHARGE_AMOUNT = 1;
export const MAX_WALLET_RECHARGE_AMOUNT = 100_000;

/** 充值弹窗所处阶段 */
export type WalletRechargePhase = "amount" | "creating" | "pending" | "paid" | "failed" | "closed";

/**
 * 解析用户输入的充值金额。
 * @param value 输入框原文
 * @returns 合法整数金额；非法或越界为 null
 */
export function parseRechargeAmount(value: string): number | null {
  const text = value.trim();
  if (!/^\d+$/.test(text)) return null;
  const amount = Number(text);
  if (!Number.isSafeInteger(amount)) return null;
  if (amount < MIN_WALLET_RECHARGE_AMOUNT || amount > MAX_WALLET_RECHARGE_AMOUNT) return null;
  return amount;
}

/**
 * 上游会话状态到弹窗阶段的映射。
 * @param status 上游状态
 * @returns 弹窗阶段；unknown 继续留在等待付款
 */
export function rechargePhase(status: WalletRechargeStatus): WalletRechargePhase {
  switch (status) {
    case "paid":
      return "paid";
    case "failed":
      return "failed";
    case "closed":
    case "refunded":
      return "closed";
    default:
      return "pending";
  }
}

/**
 * 是否是钱包任务自己的 TokenDance 余额不足。
 * 普通渠道也可能返回 provider_balance，但它不是用户能通过充值钱包解决的，必须排除。
 * @param task 任务快照
 * @returns 是否应显示钱包充值入口
 */
export function needsWalletRecharge(
  task: Pick<TaskView, "wallet" | "status" | "error_code"> | undefined,
): boolean {
  return (
    task?.wallet === true && task.status === "failed" && task.error_code === "provider_balance"
  );
}
