import { useEffect } from "react";
import { useNavigate } from "react-router";

import { useTaskSocket } from "@/utils/ws/use-task-socket";
import { useWalletStore } from "@/store/wallet";
import { setMarketingNavigator } from "@/utils/marketing/events";

/** 登录后常驻的运行时：任务 WebSocket + TokenPay 钱包状态，不渲染任何东西 */
export function WsRuntime() {
  useTaskSocket();
  const navigate = useNavigate();
  /** 营销片的提醒（写脚本结束、出片结束）里的「查看」要用路由跳转 */
  useEffect(() => {
    setMarketingNavigator((path) => void navigate(path));
    return () => setMarketingNavigator(null);
  }, [navigate]);
  useEffect(() => {
    void useWalletStore.getState().fetch();
  }, []);
  return null;
}
