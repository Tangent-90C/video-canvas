import { RouterProvider } from "react-router";

import { ConnectWalletDialog } from "@/components/wallet/connect-wallet-dialog";
import { Toaster } from "@/components/ui/sonner";
import { router } from "@/router";
import { initTheme } from "@/store";

// 挂载前先把存档里的主题贴上去，否则深色会先闪一下浅色
initTheme();

function App() {
  return (
    <>
      <RouterProvider router={router} />
      {/* 钱包连接弹窗挂在根上：任何页面撞见「需要连接钱包」都就地弹它，不用用户自己找入口 */}
      <ConnectWalletDialog />
      <Toaster />
    </>
  );
}

export default App;
