import { useEffect, useRef, useState } from "react";
import { CircleCheckBig, Loader2, OctagonAlert } from "lucide-react";
import { Link, useNavigate, useSearchParams } from "react-router";

import { Button } from "@/components/ui/button";
import { useWalletStore } from "@/store/wallet";
import { getToken } from "@/utils/storage/token";

/** 授权码交换完成后回到的默认页面 */
const DEFAULT_NEXT = "/profile?tab=wallet";

/** next 只接受站内路径，防开放重定向 */
const safeNext = (raw: string | null) =>
  raw && raw.startsWith("/") && !raw.startsWith("//") ? raw : DEFAULT_NEXT;

/**
 * TokenDance 授权完成后上游把浏览器 302 回这里（callback_url，带 flow 与一次性 code）。
 * 这一页和 /pay/return 一样只是落地页：登录态还在就直接交换授权码、保存 Key、跳回发起页；
 * 登录态丢了（token 过期）就引导先登录，登录页会带着 next 回来——但那时 code 已随页面刷新丢失，
 * 需要重新发起一次连接（上游授权码只能用一次，这里如实提示）。
 */
export default function TokenPayCallback() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const view = useWalletStore((state) => state.view);
  const fetch = useWalletStore((state) => state.fetch);
  const connect = useWalletStore((state) => state.connect);
  const loggedIn = !!getToken();
  const [error, setError] = useState("");
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
        // 刷新个人中心的资料缓存后回去，钱包 tab 直接显示已连接
        void fetch();
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
          <Loader2 className="text-muted-foreground size-10 animate-spin" />
        ) : error ? (
          <OctagonAlert className="text-destructive size-10" />
        ) : (
          <CircleCheckBig className="text-status-success size-10" />
        )}
        <h1 className="text-lg font-semibold">
          {busy ? "正在完成 TokenPay 授权…" : error ? "连接失败" : "已连接"}
        </h1>
        <p className="text-muted-foreground text-sm">
          {busy
            ? "正在用一次性授权码换取并保存你的 Key，请稍候…"
            : error
              ? error
              : `钱包已连接${view?.key_masked ? `（${view.key_masked}）` : ""}。`}
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
