import { useEffect, useState } from "react";
import { Link2, LoaderCircle, LogOut, Wallet } from "lucide-react";
import { toast } from "sonner";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { startTokenPayAuth } from "@/api/wallet";
import { useWalletStore } from "@/store/wallet";
import { openWalletRecharge } from "@/store/wallet-recharge";

/** 掩码 / 时间都是服务端给的展示值，这里只负责排版 */

/**
 * 个人中心「钱包」tab：连接自己的 TokenDance 账号。
 * 连接后，钱包渠道上的模型用你自己的 TokenDance 额度支付。
 */
export function WalletTab() {
  const view = useWalletStore((state) => state.view);
  const status = useWalletStore((state) => state.status);
  const balance = useWalletStore((state) => state.balance);
  const fetch = useWalletStore((state) => state.fetch);
  const fetchBalance = useWalletStore((state) => state.fetchBalance);
  const disconnect = useWalletStore((state) => state.disconnect);
  const [starting, setStarting] = useState(false);
  const [disconnecting, setDisconnecting] = useState(false);

  useEffect(() => {
    if (status === "idle") void fetch();
  }, [status, fetch]);

  useEffect(() => {
    if (view?.connected) void fetchBalance();
  }, [view?.connected, fetchBalance]);

  const connect = async () => {
    setStarting(true);
    try {
      // next 回到本页：授权完成后直接回到钱包 tab 看到连接状态
      const start = await startTokenPayAuth("/profile?tab=wallet");
      window.location.href = start.auth_url;
    } catch {
      setStarting(false);
    }
  };

  const disconnectNow = async () => {
    setDisconnecting(true);
    try {
      await disconnect();
      toast.success("已断开 TokenPay 钱包");
    } finally {
      setDisconnecting(false);
    }
  };

  if (status === "loading" || (status === "idle" && !view)) {
    return (
      <div
        className="bg-card ring-foreground/10 flex items-center gap-4 rounded-xl p-5 ring-1"
        aria-busy
      >
        <Skeleton className="size-10 rounded-full" />
        <div className="grid flex-1 gap-2">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="h-4 w-64" />
        </div>
      </div>
    );
  }

  if (status === "error") {
    return (
      <Alert>
        <Wallet />
        <AlertDescription>钱包状态加载失败，请稍后重试。</AlertDescription>
      </Alert>
    );
  }

  if (!view?.enabled) {
    return (
      <Alert>
        <Wallet />
        <AlertDescription>本站未启用 TokenPay 钱包连接。</AlertDescription>
      </Alert>
    );
  }

  if (!view.connected) {
    return (
      <div className="flex flex-col items-center gap-4 rounded-xl border p-8 text-center">
        <Wallet className="text-muted-foreground size-10" />
        <p className="text-muted-foreground max-w-md text-sm">
          连接你的 TokenDance 账号后，钱包渠道上的模型会使用你自己的 TokenDance
          额度支付，费用直接计入你的 TokenDance 账户；每个账号只需授权一次，随时可以断开。
        </p>
        <Button onClick={() => void connect()} disabled={starting}>
          {starting ? <LoaderCircle className="animate-spin" /> : <Link2 />}
          连接 TokenDance 账号
        </Button>
        <p className="text-muted-foreground text-xs">
          将跳转到 TokenDance 授权页（{view.app_url}），登录并确认后自动返回
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3 rounded-xl border p-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-semibold">
            <Wallet className="size-4" />
            TokenPay 钱包
            <Badge variant="secondary">已连接</Badge>
          </div>
          <div className="text-muted-foreground mt-1 space-y-0.5 text-xs">
            <p className="truncate">应用地址：{view.app_url}</p>
            {view.key_masked && <p>授权 Key：{view.key_masked}</p>}
            {view.connected_at && <p>最近连接：{formatTime(view.connected_at)}</p>}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" onClick={() => openWalletRecharge()}>
            <Wallet />
            充值
          </Button>
          <Button
            variant="outline"
            size="sm"
            disabled={disconnecting}
            onClick={() => void disconnectNow()}
          >
            {disconnecting ? <LoaderCircle className="animate-spin" /> : <LogOut />}
            断开连接
          </Button>
        </div>
      </div>
      <div className="bg-card ring-foreground/10 rounded-xl px-4 py-3 ring-1">
        <span className="text-muted-foreground text-xs">TokenDance 余额</span>
        <div className="mt-1 font-mono text-2xl font-semibold tabular-nums">
          {balance
            ? `¥${balance.balance_yuan.toLocaleString("zh-CN", { maximumFractionDigits: 2 })}`
            : "—"}
        </div>
      </div>
    </div>
  );
}

/** 把 RFC3339 时间排成「MM/DD HH:mm」，解析失败原样返回 */
function formatTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
