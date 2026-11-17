import { useEffect } from "react";
import { RouterProvider } from "react-router";

import { ConnectWalletDialog } from "@/components/wallet/connect-wallet-dialog";
import { WalletRechargeDialog } from "@/components/wallet/wallet-recharge-dialog";
import { Toaster } from "@/components/ui/sonner";
import { toast } from "sonner";
import { router } from "@/router";
import { initTheme } from "@/store";
import { WALLET_CHANNEL, useWalletStore, type WalletAuthMessage } from "@/store/wallet";

// 挂载前先把存档里的主题贴上去，否则深色会先闪一下浅色
initTheme();

/**
 * 监听钱包授权结果：授权是在新标签页里完成的，那边连上后广播通知，
 * 这里刷新钱包状态并告知用户。原页面全程没离开，草稿和上下文原样保留。
 */
function useWalletAuthChannel() {
  useEffect(() => {
    const channel = new BroadcastChannel(WALLET_CHANNEL);
    channel.onmessage = (event: MessageEvent<WalletAuthMessage>) => {
      const data = event.data;
      if (!data || data.type !== "connected") return;
      const store = useWalletStore.getState();
      if (store.view?.connected) return; // 已是连接态（弹窗里连过或提前刷新过）
      void store.fetch().then(() => {
        useWalletStore.getState().closePrompt();
        toast.success("TokenPay 钱包已连接，可以继续生成了");
      });
    };
    return () => channel.close();
  }, []);
}

function App() {
  useWalletAuthChannel();
  return (
    <>
      <RouterProvider router={router} />
      {/* 钱包连接弹窗挂在根上：任何页面撞见「需要连接钱包」都就地弹它，不用用户自己找入口 */}
      <ConnectWalletDialog />
      <WalletRechargeDialog />
      <Toaster />
    </>
  );
}

export default App;
