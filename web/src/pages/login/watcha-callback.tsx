import { useEffect, useRef, useState } from "react";
import { CircleCheckBig, LoaderCircle, OctagonAlert } from "lucide-react";
import { Link, useSearchParams } from "react-router";

import { completeWatchaLogin } from "@/api/auth";
import { Button } from "@/components/ui/button";
import { usePageTitle } from "@/hooks/use-page-title";
import { formatPageTitle } from "@/utils/page-title";
import { ApiError } from "@/utils/requests/request";
import { setToken } from "@/utils/storage/token";

/** 回调成功后只接受站内路径，和登录页的 next 规则保持一致。 */
const safeNext = (raw: string) =>
  raw.startsWith("/") && !raw.startsWith("//") && !raw.startsWith("/login") ? raw : "/";

/**
 * 观猹 OAuth2 回调落地页（/auth/watcha/callback）。
 * 观猹只把一次性 code 和 state 带到 URL；这里立刻 POST 给后端换 JWT，再把 token 存进 localStorage 并整页替换到目标页，
 * 避免 JWT 出现在地址栏、历史记录或 Referer 里。
 */
export default function WatchaCallback() {
  usePageTitle(formatPageTitle("正在登录"));
  const [params] = useSearchParams();
  const started = useRef(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const oauthError = params.get("error");
    if (oauthError) {
      setError(params.get("error_description") || "观猹授权已取消");
      return;
    }
    const flow = params.get("state") ?? "";
    const code = params.get("code") ?? "";
    if (!flow || !code) {
      setError("观猹登录回调缺少参数，请返回登录页重试");
      return;
    }
    completeWatchaLogin(flow, code)
      .then((result) => {
        setToken(result.token, result.expire_at, result.role);
        setDone(true);
        window.location.replace(safeNext(result.next));
      })
      .catch((cause) => {
        setError(cause instanceof ApiError ? cause.message : "观猹登录失败，请返回登录页重试");
      });
  }, [params]);

  const busy = !error && !done;
  return (
    <main className="bg-stage text-on-stage grid min-h-svh place-items-center p-6">
      <section className="bg-chrome/80 ring-chrome-border flex w-full max-w-sm flex-col items-center gap-3 rounded-2xl p-8 text-center ring-1 backdrop-blur-xl">
        {busy ? (
          <LoaderCircle className="text-muted-foreground size-10 animate-spin" />
        ) : error ? (
          <OctagonAlert className="text-destructive size-10" />
        ) : (
          <CircleCheckBig className="text-status-success size-10" />
        )}
        <h1 className="text-lg font-semibold">
          {busy ? "正在完成观猹登录…" : error ? "登录失败" : "登录成功"}
        </h1>
        <p className="text-muted-foreground text-sm">
          {busy ? "正在验证授权码并进入连镜，请稍候…" : error || "已登录，正在进入连镜…"}
        </p>
        {error && (
          <Button className="mt-2" render={<Link to="/login" />}>
            返回登录
          </Button>
        )}
      </section>
    </main>
  );
}
