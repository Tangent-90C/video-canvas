import { useEffect, useRef, useState } from "react";
import { CircleCheckBig, LoaderCircle, OctagonAlert } from "lucide-react";
import { Link, useNavigate, useSearchParams } from "react-router";

import { Button } from "@/components/ui/button";
import { WALLET_CHANNEL, useWalletStore, type WalletAuthMessage } from "@/store/wallet";
import { getToken } from "@/utils/storage/token";

/** 授权码交换完成后回到的默认页面 */
const DEFAULT_NEXT = "/profile?tab=wallet";

/** next 只接受站内路径，防开放重定向 */
const safeNext = (raw: string | null) =>
  raw && raw.startsWith("/") && !raw.startsWith("//") ? raw : DEFAULT_NEXT;

/**
 * TokenDance 授权完成后上游把浏览器 302 回这里（callback_url，带 flow 与一次性 code）。
 * 这一页和 /pay/return 一样只是落地页：
 * - 正常是从新标签页回来的：交换授权码、保存 Key，广播通知原页面「连上了」后关掉自己，
 *   原页面从没离开，草稿和状态全在（见 store/wallet.ts 的 startConnect）；
 * - 登录态丢了（token 过期）就引导先登录，登录页会带着 next 回来——但那时 code 已随页面刷新丢失，
 *   需要重新发起一次连接（上游授权码只能用一次，这里如实提示）；
 * - 没有 opener（用户直接打开 / 刷新了本页）时退回老行为：跳去 next 指向的页面。
 */
export default function TokenPayCallback() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const view = useWalletStore((state) => state.view);
  const fetch = useWalletStore((state) => state.fetch);
  const connect = useWalletStore((state) => state.connect);
  const loggedIn = !!getToken();
  const [error, setError] = useState("");
  /** 已连上但标签页关不掉（非脚本开的窗口）：停下显示成功态，让用户自己关 */
  const [done, setDone] = useState(false);
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    if (!loggedIn) return; // 渲染层给出「先登录」提示
    const flow = params.get("flow") ?? "";
    const code = params.get("code") ?? "";
    const next = safeNext(params.get("next"));
    if (!flow || !code) {
      setError("授权回调缺少参数，请回到个人中心重新发起连接");
      return;
    }
    (async () => {
      try {
        await connect(flow, code);
        void fetch();
        // 新标签页模式：通知原页面刷新钱包状态，然后关掉自己。原页面会弹 toast 告知用户
        if (window.opener) {
          const channel = new BroadcastChannel(WALLET_CHANNEL);
          const message: WalletAuthMessage = { type: "connected" };
          channel.postMessage(message);
          channel.close();
          window.close();
          // close 对非脚本开的窗口会失败：失败就停在本页显示成功态，用户自己关
          setTimeout(() => setDone(true), 300);
          return;
        }
        // 降级（没有 opener）：老行为，跳回发起页
        navigate(next, { replace: true });
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : "连接失败，请重试");
      }
    })();
    // connect / fetch 来自 store，引用稳定；只按回调参数触发一次
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loggedIn]);

  const busy = loggedIn && !error;

  return (
    <main className="bg-background grid min-h-svh place-items-center p-6">
      <section className="bg-card flex w-full max-w-sm flex-col items-center gap-3 rounded-2xl border p-8 text-center">
        {busy ? (
          <LoaderCircle className="text-muted-foreground size-10 animate-spin" />
        ) : done ? (
          <CircleCheckBig className="text-status-success size-10" />
        ) : error ? (
          <OctagonAlert className="text-destructive size-10" />
        ) : (
          <CircleCheckBig className="text-status-success size-10" />
        )}
        <h1 className="text-lg font-semibold">
          {busy ? "正在完成 TokenPay 授权…" : done ? "已连接" : error ? "连接失败" : "已连接"}
        </h1>
        <p className="text-muted-foreground text-sm">
          {busy
            ? "正在用一次性授权码换取并保存你的 Key，请稍候…"
            : done
              ? `钱包已连接${view?.key_masked ? `（${view.key_masked}）` : ""}，原来的页面已更新，可以关掉这个标签页。`
              : error || `钱包已连接${view?.key_masked ? `（${view.key_masked}）` : ""}。`}
        </p>
        {error ? (
          <div className="mt-2 flex gap-2">
            <Button render={<Link to="/profile?tab=wallet" />}>回到钱包页重新连接</Button>
          </div>
        ) : (
          !busy && (
            <Button className="mt-2" render={<Link to={loggedIn ? DEFAULT_NEXT : "/login"} />}>
              {loggedIn ? "回到钱包页" : "去登录"}
            </Button>
          )
        )}
      </section>
    </main>
  );
}
