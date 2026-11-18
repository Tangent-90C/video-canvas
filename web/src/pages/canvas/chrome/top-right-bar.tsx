import { useMemo, useState } from "react";
import NumberFlow from "@number-flow/react";
import { useNavigate } from "react-router";
import {
  Keyboard,
  LogOut,
  Monitor,
  Moon,
  MoreHorizontal,
  Settings,
  ShieldCheck,
  Sun,
  User,
  Wallet,
  Loader2,
} from "lucide-react";

import {
  ChromeButton,
  ChromePill,
  ChromeSeparator,
  ChromeTooltip,
} from "@/components/canvas/chrome/chrome";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Button } from "@/components/ui/button";
import { UserAvatar } from "@/components/user-avatar";
import type { ThemeSetting } from "@/lib/theme";
import { cn } from "@/lib/utils";
import { useSettingsStore } from "@/store";
import { useMeStore } from "@/store/me";
import { useWalletStore } from "@/store/wallet";
import { openWalletRecharge } from "@/store/wallet-recharge";
import { useWsStore, type ConnectionState } from "@/store/ws";
import { canEnterAdmin } from "@/utils/admin/role";
import { displayName } from "@/utils/profile/profile-rules";
import { logout } from "@/utils/storage/session";
import { getRole, getToken } from "@/utils/storage/token";
import { toast } from "sonner";

const THEMES: { value: ThemeSetting; label: string; icon: typeof Sun }[] = [
  { value: "system", label: "跟随系统", icon: Monitor },
  { value: "dark", label: "深色", icon: Moon },
  { value: "light", label: "浅色", icon: Sun },
];

const WS_TEXT: Record<ConnectionState, string> = {
  idle: "实时进度未连接",
  connecting: "正在连接实时进度",
  connected: "实时进度已连接",
  reconnecting: "实时进度已断开，正在重连",
  closed: "实时进度已断开",
};

/**
 * 从登录令牌里读出用户名，读不出就返回 null（令牌是不透明串时）。
 * 只在 /me 资料还没拉到时兜底，正常显示用 me.nickname || me.username
 */
export function useUsername() {
  return useMemo(() => {
    try {
      const payload = getToken()?.split(".")[1];
      if (!payload) return null;
      const json = JSON.parse(atob(payload.replace(/-/g, "+").replace(/_/g, "/")));
      const name = json.username ?? json.name ?? json.sub;
      return typeof name === "string" && name ? name : null;
    } catch {
      return null;
    }
  }, []);
}

/** 当前账号能否进入管理后台：读登录时存下的角色，普通用户不显示入口 */
export function useCanEnterAdmin() {
  return useMemo(() => canEnterAdmin(getRole()), []);
}

/** 把 TokenDance 余额（元）格式化成 1 位小数的短串：12.5 → "12.5"，0.03 → "0.03" */
function formatYuan(value: number) {
  if (value >= 100) return String(Math.round(value));
  return String(Math.round(value * 100) / 100);
}

/** 钱包按钮的内容：连接状态点 + 钱包图标 + TokenDance 余额；未连接时显示引导 */
function WalletFace({ showPendingDot }: { showPendingDot: boolean }) {
  const connection = useWsStore((state) => state.connection);
  const view = useWalletStore((state) => state.view);
  const balance = useWalletStore((state) => state.balance);
  const down = connection === "reconnecting" || connection === "closed";
  const pending = connection === "connecting" || connection === "idle";

  if (!view?.connected) {
    return (
      <>
        <span
          title={WS_TEXT[connection]}
          className={cn(
            "size-1.5 rounded-full",
            down
              ? "bg-status-warning ring-status-warning/20 animate-pulse ring-3"
              : pending
                ? "bg-muted-foreground/50"
                : "bg-status-success ring-status-success/15 ring-3",
          )}
        />
        <Wallet className="size-4.5!" strokeWidth={2} />
        <span className="text-[13px]">连接钱包</span>
        {showPendingDot ? (
          <span
            title="有一笔待支付的充值订单"
            className="bg-status-warning border-background absolute -top-0.5 -right-0.5 size-2.5 rounded-full border-2"
          />
        ) : null}
      </>
    );
  }
  return (
    <>
      <span
        title={WS_TEXT[connection]}
        className={cn(
          "size-1.5 rounded-full",
          down
            ? "bg-status-warning ring-status-warning/20 animate-pulse ring-3"
            : pending
              ? "bg-muted-foreground/50"
              : "bg-status-success ring-status-success/15 ring-3",
        )}
      />
      <Wallet className="size-4.5!" strokeWidth={2} />
      {balance ? (
        <NumberFlow value={balance.balance_yuan} className="tabular-nums" />
      ) : (
        <span className="text-muted-foreground">—</span>
      )}
      {balance?.stale && (
        <span title="余额可能不是最新" className="bg-status-warning size-1.5 rounded-full" />
      )}
    </>
  );
}

/**
 * 钱包菜单（不带胶囊）：放进任意 ChromePill 里用。
 * 钱包已连接：显示 TokenDance 余额与已消耗、Key 掩码、连接时间，可断开；
 * 未连接：点了直接弹全局连接弹窗（新标签页授权）；
 * 本站未启用钱包时也明确引导，不回退到任何平台积分。
 */
export function WalletMenu() {
  const view = useWalletStore((state) => state.view);
  const status = useWalletStore((state) => state.status);
  const balance = useWalletStore((state) => state.balance);
  const fetchBalance = useWalletStore((state) => state.fetchBalance);
  const openPrompt = useWalletStore((state) => state.openPrompt);
  const disconnect = useWalletStore((state) => state.disconnect);
  const [disconnecting, setDisconnecting] = useState(false);
  const connection = useWsStore((state) => state.connection);
  const down = connection === "reconnecting" || connection === "closed";

  // 钱包状态还没拉到时先显示加载态：本站可能未启用钱包，不能闪一下「连接钱包」
  if (status === "idle" || status === "loading") {
    return (
      <ChromeButton aria-label="钱包状态加载中" className={WALLET_BUTTON_CLASS} disabled>
        <Wallet className="size-4.5!" strokeWidth={2} />
        <span className="text-muted-foreground">—</span>
      </ChromeButton>
    );
  }

  // 未启用钱包的部署：明确提示，不回退到任何平台积分。
  if (!view?.enabled) {
    return (
      <ChromeButton aria-label="TokenPay 钱包未启用" className={WALLET_BUTTON_CLASS} disabled>
        <Wallet className="size-4.5!" strokeWidth={2} />
        <span className="text-muted-foreground">钱包未启用</span>
      </ChromeButton>
    );
  }

  if (view.connected) {
    return (
      <Popover
        onOpenChange={(open) => {
          if (open) void fetchBalance();
        }}
      >
        <PopoverTrigger
          render={
            <ChromeButton
              aria-label={`TokenPay 钱包，余额 ${balance ? formatYuan(balance.balance_yuan) : "未知"} 元，${WS_TEXT[connection]}`}
              className={WALLET_BUTTON_CLASS}
            >
              <WalletFace showPendingDot={false} />
            </ChromeButton>
          }
        />
        <PopoverContent align="end" sideOffset={10} className="w-72 gap-0 rounded-xl p-0">
          <div className="flex items-baseline gap-2 px-4 pt-4 pb-3">
            <span className="font-mono text-2xl font-semibold tabular-nums">
              {balance ? `¥${formatYuan(balance.balance_yuan)}` : "—"}
            </span>
            <span className="text-muted-foreground text-xs">
              TokenDance 余额{balance?.stale ? "（可能不是最新）" : ""}
            </span>
          </div>
          <dl className="border-border grid grid-cols-[1fr_auto] gap-y-2 border-t px-4 py-3 text-xs">
            <dt className="text-muted-foreground">已消耗</dt>
            <dd className="font-mono tabular-nums">
              {balance ? `¥${formatYuan(balance.used_yuan)}` : "—"}
            </dd>
            <dt className="text-muted-foreground">授权 Key</dt>
            <dd className="font-mono">{view.key_masked || "—"}</dd>
            <dt className="text-muted-foreground">最近连接</dt>
            <dd className="font-mono">
              {view.connected_at ? new Date(view.connected_at).toLocaleDateString() : "—"}
            </dd>
          </dl>
          <div className="border-border flex items-center justify-end gap-2 border-t px-4 py-2.5">
            <Button variant="outline" size="sm" onClick={() => openWalletRecharge()}>
              <Wallet />
              充值
            </Button>
            <Button
              variant="ghost"
              size="sm"
              disabled={disconnecting}
              onClick={() => {
                setDisconnecting(true);
                void disconnect()
                  .then(() => toast.success("已断开 TokenPay 钱包"))
                  .catch(() => toast.error("断开失败，请稍后重试"))
                  .finally(() => setDisconnecting(false));
              }}
            >
              {disconnecting ? <Loader2 className="animate-spin" /> : <LogOut />}
              断开
            </Button>
          </div>
          <p
            className={cn(
              "border-border flex items-center gap-2 border-t px-4 py-2.5 text-xs",
              down ? "text-status-warning" : "text-muted-foreground",
            )}
          >
            {WS_TEXT[connection]}
          </p>
        </PopoverContent>
      </Popover>
    );
  }

  // 已启用但未连接：点了弹全局连接弹窗
  return (
    <ChromeButton
      aria-label={`TokenPay 钱包未连接，点击连接，${WS_TEXT[connection]}`}
      className={WALLET_BUTTON_CLASS}
      onClick={() => openPrompt()}
    >
      <WalletFace showPendingDot={false} />
    </ChromeButton>
  );
}

/** 钱包按钮的公共样式：余额是元（可能带小数），用等宽数字对齐 */
const WALLET_BUTTON_CLASS =
  "text-foreground relative h-9 gap-2 pr-3 pl-2.5 font-mono text-sm font-semibold";

/** 钱包胶囊：只有 TokenPay 钱包一个按钮，首页顶栏用；画布右上角把钱包和头像合在一个胶囊里 */
export function WalletPill() {
  return (
    <ChromePill>
      <WalletMenu />
    </ChromePill>
  );
}

/** 右上角：更多菜单、积分、账户 */
export function TopRightBar({
  onOpenSettings,
  onOpenShortcuts,
}: {
  onOpenSettings: () => void;
  onOpenShortcuts: () => void;
}) {
  const navigate = useNavigate();
  const theme = useSettingsStore((state) => state.theme);
  const updateSettings = useSettingsStore((state) => state.updateSettings);
  const tokenUsername = useUsername();
  const me = useMeStore((state) => state.me);
  const username = displayName(me) || tokenUsername;
  const showAdmin = useCanEnterAdmin();

  return (
    <>
      <ChromePill>
        <DropdownMenu modal={false}>
          <ChromeTooltip label="更多" side="bottom">
            <DropdownMenuTrigger render={<ChromeButton aria-label="更多" />}>
              <MoreHorizontal />
            </DropdownMenuTrigger>
          </ChromeTooltip>
          <DropdownMenuContent align="end" sideOffset={10} className="w-56">
            <DropdownMenuItem onClick={onOpenSettings}>
              <Settings />
              画布设置
            </DropdownMenuItem>
            <DropdownMenuItem onClick={onOpenShortcuts}>
              <Keyboard />
              快捷键
              <DropdownMenuShortcut>?</DropdownMenuShortcut>
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                {theme === "light" ? <Sun /> : theme === "dark" ? <Moon /> : <Monitor />}
                主题
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent className="w-40">
                <DropdownMenuRadioGroup
                  value={theme}
                  onValueChange={(value) => updateSettings("theme", value as ThemeSetting)}
                >
                  {THEMES.map((item) => (
                    <DropdownMenuRadioItem key={item.value} value={item.value}>
                      <item.icon />
                      {item.label}
                    </DropdownMenuRadioItem>
                  ))}
                </DropdownMenuRadioGroup>
              </DropdownMenuSubContent>
            </DropdownMenuSub>
          </DropdownMenuContent>
        </DropdownMenu>
      </ChromePill>

      {/* 钱包和头像合在一个胶囊里，头像 32px；钱包余额是唯一计费入口 */}
      <ChromePill>
        <WalletMenu />
        <ChromeSeparator />
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger
            render={<ChromeButton aria-label="账户" className="size-9 min-w-9 px-0" />}
          >
            {me ? (
              <UserAvatar userId={me.id} name={username ?? ""} src={me.avatarUrl} size={32} />
            ) : (
              <span className="bg-muted-foreground grid size-8 place-items-center rounded-full text-sm font-semibold text-white uppercase">
                {username?.slice(0, 1) ?? "我"}
              </span>
            )}
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" sideOffset={10} className="w-52">
            {username && (
              <DropdownMenuGroup>
                <DropdownMenuLabel className="truncate">{username}</DropdownMenuLabel>
              </DropdownMenuGroup>
            )}
            <DropdownMenuItem onClick={() => navigate("/profile")}>
              <User />
              个人中心
            </DropdownMenuItem>
            {showAdmin && (
              <DropdownMenuItem onClick={() => navigate("/admin/ai")}>
                <ShieldCheck />
                管理后台
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onClick={logout}>
              <LogOut />
              退出登录
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </ChromePill>
    </>
  );
}
