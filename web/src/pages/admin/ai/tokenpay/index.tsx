import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Loader2, Wallet, WandSparkles } from "lucide-react";
import { toast } from "sonner";

import {
  createChannel,
  listChannelOptions,
  syncChannelModels,
  updateChannel,
} from "@/api/admin/ai";
import { getAuthConfig, type AuthConfig } from "@/api/auth";
import type { ChannelOption } from "@/api/admin/ai/type.d";
import { FormField } from "@/components/admin-ui/form-field";
import { Notice } from "@/components/admin-ui/notice";
import { PageHeader, PageHeaderHeading, PageHeaderTitle } from "@/components/admin-ui/page-header";
import { errorMessage } from "@/utils/admin/errors";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { useAdminStore } from "@/store/admin";
import { canManageInfra } from "@/utils/admin/role";

import { ReadOnlyNotice } from "../../shared";
import { useAdminCatalogOptions } from "../../use-admin";
import { ChannelModelsPanel } from "../models/channel-models-panel";

/** TokenPay 网关地址：渠道的 Base URL 固定指向它，默认值给表单预填 */
export const TOKENPAY_BASE_URL = "https://tokendance.space/gateway";

/**
 * TokenPay 钱包（/admin/ai/tokenpay）：钱包是一等公民的独立配置页，不和通用渠道混在一起。
 * 底层仍是一个自动管理的钱包渠道（wallet_enabled），这里负责它的全部配置：
 * 网关地址、X-App-URL 归因，以及「保存并同步全部模型」一键上架。
 * TokenDance 模型目录是公开接口，钱包渠道不保存产品方 Key；用户调用使用自己的钱包 Key。
 * 页面读公开配置（tokenpay_enabled / tokenpay_app_url）与渠道清单；写操作只有 super_admin。
 */
export default function TokenPayPage() {
  const { catalog } = useAdminCatalogOptions();
  const { plugins, pluginsStatus } = catalog;
  const role = useAdminStore((state) => state.role);
  const canWrite = canManageInfra(role);
  const [auth, setAuth] = useState<AuthConfig | null>(null);
  /** 钱包渠道：catalog 里已过滤掉它，这里单独拉全量再取 */
  const [walletChannel, setWalletChannel] = useState<ChannelOption | null>(null);
  const [channelLoading, setChannelLoading] = useState(true);
  /** 面板的刷新令牌：保存 / 同步后递增，让模型表重拉 */
  const [reloadToken, setReloadToken] = useState(0);

  const reloadWallet = useCallback(async () => {
    try {
      const all = await listChannelOptions();
      setWalletChannel(all.find((item) => item.wallet_enabled) ?? null);
      setChannelLoading(false);
    } catch {
      setChannelLoading(false);
    }
  }, []);

  useEffect(() => {
    void getAuthConfig()
      .then(setAuth)
      .catch(() => setAuth(null));
  }, []);
  useEffect(() => {
    void reloadWallet();
  }, [reloadWallet]);

  const refresh = useCallback(() => {
    void reloadWallet();
    setReloadToken((n) => n + 1);
  }, [reloadWallet]);

  const loading = channelLoading;
  const platformEnabled = auth?.tokenpay_enabled ?? false;
  const appURL = auth?.tokenpay_app_url ?? "";

  return (
    <DataPageWrapper>
      <PageHeader>
        <PageHeaderHeading>
          <PageHeaderTitle>
            <Wallet className="size-5" />
            TokenPay 钱包
          </PageHeaderTitle>
        </PageHeaderHeading>
      </PageHeader>
      {loading ? (
        <div className="flex flex-col gap-4" aria-busy="true">
          <Skeleton className="h-64" />
        </div>
      ) : !platformEnabled ? (
        <Notice tone="warning">
          本站未启用 TokenPay 钱包：部署时需要设置环境变量 <code>APP_TOKENPAY_APP_URL</code>
          （本站对外地址），设置并重启后这里才能配置。
        </Notice>
      ) : (
        <div className="flex min-h-0 flex-col gap-5">
          <WalletSettings
            channel={walletChannel}
            appURL={appURL}
            canWrite={canWrite}
            onSaved={refresh}
          />
          {walletChannel && (
            <ChannelModelsPanel
              key={walletChannel.key}
              channel={walletChannel}
              channels={[walletChannel]}
              plugins={plugins}
              pluginsStatus={pluginsStatus}
              canWriteChannel={canWrite}
              reloadToken={reloadToken}
              onChanged={refresh}
              onPull={() => toast.info("用上面的「保存并同步全部模型」即可拉取并上架全部模型")}
            />
          )}
        </div>
      )}
    </DataPageWrapper>
  );
}

/** 钱包渠道的配置卡：网关地址、归因地址、启用开关，保存时可顺带同步全部模型 */
export function WalletSettings({
  channel,
  appURL,
  canWrite,
  onSaved,
}: {
  channel: ChannelOption | null;
  appURL: string;
  canWrite: boolean;
  onSaved: () => void;
}) {
  const [baseUrl, setBaseUrl] = useState(channel?.base_url ?? TOKENPAY_BASE_URL);
  const [appUrl, setAppUrl] = useState(channel?.headers?.["X-App-URL"] || appURL);
  const [enabled, setEnabled] = useState(channel?.enabled ?? true);
  const [saving, setSaving] = useState(false);
  const [syncing, setSyncing] = useState(false);

  // 渠道加载完成（异步）后回填一次表单：只在从「未配置」变「已配置」时整体重置
  const channelKey = channel?.key ?? "";
  const [loadedKey, setLoadedKey] = useState(channelKey);
  useEffect(() => {
    if (loadedKey === channelKey) return;
    setLoadedKey(channelKey);
    setBaseUrl(channel?.base_url ?? TOKENPAY_BASE_URL);
    setAppUrl(channel?.headers?.["X-App-URL"] || appURL);
    setEnabled(channel?.enabled ?? true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [channelKey]);

  const dirty =
    !channel ||
    baseUrl !== channel.base_url ||
    appUrl !== (channel.headers?.["X-App-URL"] ?? "") ||
    enabled !== channel.enabled;

  const save = async (withSync: boolean) => {
    if (!canWrite || saving) return;
    if (!baseUrl.trim()) {
      toast.error("请填写网关地址");
      return;
    }
    setSaving(true);
    try {
      const headers: Record<string, string> = appUrl.trim() ? { "X-App-URL": appUrl.trim() } : {};
      if (!channel) {
        await createChannel({
          key: "tokenpay",
          name: "TokenPay 钱包",
          base_url: baseUrl.trim(),
          headers,
          enabled,
          wallet_enabled: true,
        });
      } else {
        await updateChannel(channel.key, { base_url: baseUrl.trim(), headers, enabled });
      }
      if (withSync) {
        setSyncing(true);
        const id = toast.loading("已保存，正在同步全部模型…");
        try {
          const res = await syncChannelModels(channel?.key ?? "tokenpay");
          const disabled = res.created.filter((item) => !item.enabled).length;
          const parts = [`新建 ${res.created.length} 个模型`];
          if (disabled > 0) parts.push(`${disabled} 个未能自动启用（原因见模型列表）`);
          if (res.skipped.length > 0) parts.push(`跳过 ${res.skipped.length} 个`);
          toast.success(`同步完成：${parts.join("，")}`, { id });
        } catch (cause) {
          toast.error(errorMessage(cause, "同步失败"), { id });
        } finally {
          setSyncing(false);
        }
      } else {
        toast.success("TokenPay 钱包配置已保存");
      }
      onSaved();
    } catch (cause) {
      toast.error(errorMessage(cause, "保存失败"));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card>
      <CardContent className="grid gap-4">
        {!canWrite && <ReadOnlyNotice what="TokenPay 钱包配置" />}
        <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
          <span>
            状态：
            {channel ? (
              <span className="text-foreground">
                已配置（{channel.enabled ? "启用中" : "已停用"}）
              </span>
            ) : (
              <span className="text-foreground">未配置——填写网关和归因地址后即可开通全部模型</span>
            )}
          </span>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <FormField
            label="网关地址"
            htmlFor="tokenpay-base-url"
            hint="TokenDance 网关，一般不用改"
          >
            <Input
              id="tokenpay-base-url"
              value={baseUrl}
              disabled={!canWrite || saving}
              onChange={(event) => setBaseUrl(event.target.value)}
            />
          </FormField>
          <FormField
            label="X-App-URL 归因地址"
            htmlFor="tokenpay-app-url"
            hint="调用量按它计入本站分润，默认取本站对外地址"
          >
            <Input
              id="tokenpay-app-url"
              value={appUrl}
              disabled={!canWrite || saving}
              onChange={(event) => setAppUrl(event.target.value)}
            />
          </FormField>
          <div className="flex items-center gap-2 sm:pt-6">
            <Switch
              id="tokenpay-enabled"
              checked={enabled}
              disabled={!canWrite || saving}
              onCheckedChange={setEnabled}
            />
            <label htmlFor="tokenpay-enabled" className="text-sm font-medium">
              启用钱包渠道
            </label>
          </div>
        </div>
      </CardContent>
      <CardFooter className="gap-2">
        <Button
          variant="outline"
          disabled={!canWrite || !dirty || saving || syncing}
          onClick={() => void save(false)}
        >
          {saving && <Loader2 className="animate-spin" />}
          保存
        </Button>
        <Button disabled={!canWrite || saving || syncing} onClick={() => void save(true)}>
          {syncing ? <Loader2 className="animate-spin" /> : <WandSparkles />}
          {channel ? "保存并同步全部模型" : "保存并开通（自动上架全部模型）"}
        </Button>
      </CardFooter>
    </Card>
  );
}

/** 页面容器：与其它管理页一致的内容宽度 */
function DataPageWrapper({ children }: { children: ReactNode }) {
  return <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto pb-8">{children}</div>;
}
