import { useEffect } from "react";
import { CheckCircle2, LoaderCircle, QrCode, RefreshCw, Wallet } from "lucide-react";

import { CheckoutQr } from "@/components/wallet/checkout-qr";
import { Button, buttonVariants } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { useIsMobile } from "@/hooks/use-mobile";
import { useWalletRechargeStore } from "@/store/wallet-recharge";
import { useWalletStore } from "@/store/wallet";
import {
  MAX_WALLET_RECHARGE_AMOUNT,
  MIN_WALLET_RECHARGE_AMOUNT,
  RECHARGE_PRESETS,
  parseRechargeAmount,
} from "@/utils/wallet/recharge";

/** 充值状态轮询间隔：TokenDance 没有 WebSocket，弹窗打开时按固定间隔查。 */
const POLL_INTERVAL_MS = 3000;

/** 余额（元）去掉无意义的小数零 */
function yuan(value: number | undefined): string {
  if (value === undefined) return "—";
  return `¥${value.toLocaleString("zh-CN", { maximumFractionDigits: 2 })}`;
}

/**
 * 全局 TokenDance 充值弹窗：钱包入口与余额不足失败卡都打开这里。
 * 桌面端把 payment_url 画成二维码；移动端只提供 alipay_url，不直接打开二维码内容。
 */
export function WalletRechargeDialog() {
  const open = useWalletRechargeStore((state) => state.open);
  const phase = useWalletRechargeStore((state) => state.phase);
  const amountInput = useWalletRechargeStore((state) => state.amountInput);
  const session = useWalletRechargeStore((state) => state.session);
  const errorMessage = useWalletRechargeStore((state) => state.errorMessage);
  const hasContinuation = useWalletRechargeStore((state) => state.hasContinuation);
  const close = useWalletRechargeStore((state) => state.close);
  const setAmountInput = useWalletRechargeStore((state) => state.setAmountInput);
  const create = useWalletRechargeStore((state) => state.create);
  const refresh = useWalletRechargeStore((state) => state.refresh);
  const markExpired = useWalletRechargeStore((state) => state.markExpired);
  const reset = useWalletRechargeStore((state) => state.reset);
  const continueAfterPaid = useWalletRechargeStore((state) => state.continueAfterPaid);
  const balance = useWalletStore((state) => state.balance);
  const fetchBalance = useWalletStore((state) => state.fetchBalance);
  const isMobile = useIsMobile();
  const sessionId = session?.id;
  const expiresAt = session?.expiresAt;

  useEffect(() => {
    if (open) void fetchBalance();
  }, [open, fetchBalance]);

  useEffect(() => {
    if (!open || phase !== "pending" || !sessionId || expiresAt === undefined) return;
    const check = async (atExpiry = false) => {
      await refresh(true);
      if (atExpiry) markExpired();
    };
    const timer = window.setInterval(() => void check(), POLL_INTERVAL_MS);
    const onVisible = () => {
      if (document.visibilityState === "visible") void check();
    };
    document.addEventListener("visibilitychange", onVisible);
    const remaining = Number.isFinite(expiresAt) ? expiresAt - Date.now() : null;
    const expiry =
      remaining === null || remaining < 0
        ? undefined
        : window.setTimeout(() => void check(true), remaining);
    if (remaining !== null && remaining <= 0) void check(true);
    return () => {
      window.clearInterval(timer);
      if (expiry !== undefined) window.clearTimeout(expiry);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [open, phase, sessionId, expiresAt, refresh, markExpired]);

  const amount = parseRechargeAmount(amountInput);
  const amountValid =
    amount !== null && amount >= MIN_WALLET_RECHARGE_AMOUNT && amount <= MAX_WALLET_RECHARGE_AMOUNT;

  return (
    <Dialog open={open} onOpenChange={(next) => !next && close()}>
      <DialogContent className="max-h-[min(88svh,640px)] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Wallet className="size-5" />
            TokenDance 充值
          </DialogTitle>
          <DialogDescription>
            当前余额 {yuan(balance?.balance_yuan)}。充值会直接进入你的 TokenDance 钱包。
          </DialogDescription>
        </DialogHeader>

        {phase === "amount" && (
          <div className="grid gap-4">
            <div className="grid grid-cols-4 gap-2">
              {RECHARGE_PRESETS.map((preset) => (
                <button
                  key={preset}
                  type="button"
                  aria-pressed={amountInput === String(preset)}
                  onClick={() => setAmountInput(String(preset))}
                  className="bg-chrome hover:bg-chrome-hover aria-pressed:bg-chrome-hover ring-chrome-border rounded-full px-3 py-2 text-sm font-semibold ring-1 transition-colors"
                >
                  ¥{preset}
                </button>
              ))}
            </div>
            <label className="grid gap-1.5 text-xs font-medium">
              自定义金额（元）
              <Input
                type="number"
                inputMode="numeric"
                min={MIN_WALLET_RECHARGE_AMOUNT}
                max={MAX_WALLET_RECHARGE_AMOUNT}
                step={1}
                value={amountInput}
                onChange={(event) => setAmountInput(event.currentTarget.value)}
                className="rounded-full bg-chrome-hover border-chrome-border"
              />
            </label>
            {errorMessage && <p className="text-destructive text-xs">{errorMessage}</p>}
            <div className="flex justify-end gap-2">
              <Button variant="ghost" onClick={close}>
                取消
              </Button>
              <Button disabled={!amountValid} onClick={() => void create()}>
                去支付 {amountValid ? yuan(amount) : ""}
              </Button>
            </div>
          </div>
        )}

        {phase === "creating" && (
          <div
            className="text-muted-foreground grid place-items-center gap-2 py-10 text-sm"
            aria-busy
          >
            <LoaderCircle className="animate-spin" />
            正在创建充值会话…
          </div>
        )}

        {phase === "pending" && session && (
          <div className="grid gap-4">
            <div className="flex items-center justify-between rounded-xl bg-chrome px-3 py-2 text-sm ring-1 ring-chrome-border">
              <span>本次充值</span>
              <b className="font-mono">{yuan(session.amount)}</b>
            </div>
            {!isMobile && session.paymentUrl ? (
              <CheckoutQr mode="qrcode" value={session.paymentUrl} expired={false} />
            ) : (
              <div className="text-muted-foreground bg-muted grid place-items-center gap-2 rounded-xl px-4 py-8 text-center text-sm">
                <QrCode className="size-6" />
                {session.alipayUrl ? "请在当前设备打开支付宝完成支付" : "请在电脑上扫码完成充值"}
              </div>
            )}
            {session.alipayUrl && (
              <a
                href={session.alipayUrl}
                className={buttonVariants({ variant: "outline", className: "w-full" })}
              >
                打开支付宝支付
              </a>
            )}
            {errorMessage && <p className="text-destructive text-xs">{errorMessage}</p>}
            <div className="flex justify-end gap-2">
              <Button variant="ghost" onClick={close}>
                稍后支付
              </Button>
              <Button variant="outline" onClick={() => void refresh(false)}>
                <RefreshCw />
                我已完成支付
              </Button>
            </div>
          </div>
        )}

        {phase === "paid" && session && (
          <div className="grid justify-items-center gap-3 py-4 text-center">
            <CheckCircle2 className="text-status-success size-10" />
            <div>
              <b className="text-lg">已充值 {yuan(session.amount)}</b>
              <p className="text-muted-foreground mt-1 text-xs">
                当前余额 {yuan(balance?.balance_yuan)}
              </p>
            </div>
            <Button className="w-full" onClick={() => void continueAfterPaid()}>
              {hasContinuation ? "已充值，继续生成" : "完成"}
            </Button>
          </div>
        )}

        {(phase === "failed" || phase === "closed") && (
          <div className="grid justify-items-center gap-3 py-4 text-center">
            <QrCode className="text-muted-foreground size-9" />
            <p className="text-sm">
              {errorMessage || (phase === "failed" ? "充值未完成" : "充值会话已关闭")}
            </p>
            <Button className="w-full" onClick={reset}>
              重新创建
            </Button>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
