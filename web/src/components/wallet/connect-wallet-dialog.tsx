import { useEffect, useState } from "react";
import { Link2, LoaderCircle, Wallet } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useComposerStore } from "@/store/composer";
import { useWalletStore } from "@/store/wallet";
import { saveComposerDraft } from "@/utils/home/carry";

/**
 * 全局「连接 TokenPay 钱包」弹窗：用户在任何地方撞见「需要连接钱包」时，就地在这里完成授权，
 * 不用自己跑去个人中心。生成被钱包拦住（未连接）、点模型上的钱包标记、个人中心都走这一个弹窗。
 * 挂在 App 根上（见 App.tsx），开关由 store 的 promptOpen 驱动。
 */
export function ConnectWalletDialog() {
  const open = useWalletStore((state) => state.promptOpen);
  const view = useWalletStore((state) => state.view);
  const status = useWalletStore((state) => state.status);
  const closePrompt = useWalletStore((state) => state.closePrompt);
  const startConnect = useWalletStore((state) => state.startConnect);
  const [starting, setStarting] = useState(false);

  // 打开时状态还没拉过（例如从画布直接进来）就拉一次：弹窗内容按 enabled / connected 显示
  useEffect(() => {
    if (open && status === "idle") void useWalletStore.getState().fetch();
  }, [open, status]);

  const connect = () => {
    setStarting(true);
    // 先同步开空白标签：await 之后开窗会被浏览器当弹窗拦掉。被拦（null）时 store 内部
    // 会自动降级成当前页跳转，并按老办法暂存草稿。
    const win = window.open("", "_blank");
    // 降级路径下原页面要离开：把没发送的草稿暂存，回来后按来源页恢复
    const composer = useComposerStore.getState();
    if (composer.text.trim()) {
      saveComposerDraft({
        text: composer.text,
        refs: composer.refs
          .filter((ref) => ref.status === "done")
          .map((ref) => ({
            kind: ref.kind,
            id: ref.id,
            assetId: ref.assetId,
            url: ref.url,
            name: ref.name,
          })),
        from: `${window.location.pathname}${window.location.search}`,
      });
    }
    void (async () => {
      try {
        await startConnect(undefined, win);
      } catch (cause) {
        toast.error(cause instanceof Error ? cause.message : "发起授权失败，请稍后重试");
        setStarting(false);
      }
    })();
  };

  // 已连接（例如授权回来前在别处连上了）：直接关掉，别让用户看到无意义的弹窗
  useEffect(() => {
    if (open && view?.connected) closePrompt();
  }, [open, view?.connected, closePrompt]);

  const loading = status === "loading" || (status === "idle" && !view);
  const disabled = !view?.enabled || loading || starting || !!view?.connected;

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? undefined : closePrompt())}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Wallet className="size-5" />
            连接 TokenPay 钱包
          </DialogTitle>
          <DialogDescription>
            这个模型用你自己的 TokenDance 额度支付。连接后即可生成，不消耗平台积分。
          </DialogDescription>
        </DialogHeader>

        {loading ? (
          <div className="text-muted-foreground flex items-center gap-2 py-6 text-sm" aria-busy>
            <LoaderCircle className="animate-spin" />
            正在读取钱包状态…
          </div>
        ) : !view?.enabled ? (
          <p className="text-muted-foreground py-2 text-sm">本站未启用 TokenPay 钱包连接。</p>
        ) : (
          <div className="text-muted-foreground space-y-2 text-sm">
            <p>连接你的 TokenDance 账号后，钱包渠道上的模型会使用你自己的额度支付：</p>
            <ul className="list-disc space-y-1 pl-5">
              <li>每个账号只需授权一次，随时可以在个人中心断开</li>
              <li>Key 的额度与消费记录由你在 TokenDance 控制台管理</li>
            </ul>
            {view?.app_url && (
              <p className="text-xs">将跳转到 TokenDance 授权页（{view.app_url}）完成确认</p>
            )}
          </div>
        )}

        <DialogFooter>
          <Button variant="ghost" onClick={closePrompt}>
            稍后再说
          </Button>
          <Button disabled={disabled} onClick={() => void connect()}>
            {starting ? <LoaderCircle className="animate-spin" /> : <Link2 />}
            在新标签页中连接
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
