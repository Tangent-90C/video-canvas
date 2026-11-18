import { motion } from "motion/react";
import { ChevronDown, Link2, Wallet } from "lucide-react";

import type { ModelInfo } from "@/api/model/type";
import { VendorAvatar } from "@/components/admin-ui/vendor-avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { TAP } from "@/lib/motion";
import { cn } from "@/lib/utils";
import { useWalletStore } from "@/store/wallet";
import { modelPriceLabel } from "@/utils/pricing/model-price";

/** 工具条按钮的公共样式 */
const BAR_BUTTON =
  "text-foreground hover:bg-muted focus-visible:ring-ring/50 inline-flex h-8 max-w-48 items-center gap-1.5 rounded-[10px] px-2.5 text-[13px] outline-none focus-visible:ring-3 disabled:opacity-60";

/**
 * 输入卡片里的模型选择：按钮直接显示当前模型名（没有「自动」，用户必须清楚自己在用哪个模型），
 * 菜单列出该种类全部已发布的模型：名称、简介和起价。
 * 钱包模型（TokenPay 渠道）特殊对待：已连接时带「钱包」徽标正常选；没连接时也列出来，
 * 但点了不选中，直接弹连接弹窗——让用户在这里就连上，而不是提交被拦下再自己找入口。
 * @param models 该种类的模型清单（含未连接时不可用的钱包模型）
 * @param status 清单加载状态：没回来时按钮禁用并写明原因
 * @param value 当前选中的模型
 * @param onChange 选择模型
 * @param side 菜单弹出方向
 */
export function ModelMenu({
  models,
  status,
  value,
  onChange,
  side = "bottom",
}: {
  models: ModelInfo[];
  status: "idle" | "loading" | "ready" | "error";
  value: ModelInfo | undefined;
  onChange: (key: string) => void;
  side?: "top" | "bottom";
}) {
  // 钱包连接状态决定钱包模型是「带徽标可选」还是「点了弹连接窗」，hooks 要在早退之前
  const walletConnected = useWalletStore((state) => !!state.view?.connected);
  const openPrompt = useWalletStore((state) => state.openPrompt);

  if (!value) {
    const label =
      status === "ready" ? "暂无可用模型" : status === "error" ? "模型加载失败" : "加载模型…";
    return (
      <button type="button" disabled className={BAR_BUTTON}>
        <span className="truncate">{label}</span>
      </button>
    );
  }
  const needConnect = models.filter((item) => item.wallet && !walletConnected);
  return (
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger
        render={<motion.button type="button" whileTap={TAP} />}
        aria-label={`选择模型，当前 ${value.label}`}
        className={cn(BAR_BUTTON, "group/model")}
      >
        <VendorAvatar
          vendor={value.vendor}
          name={value.label}
          seed={value.key}
          className="size-5 rounded-md text-[10px]"
        />
        <span className="truncate max-md:hidden">{value.label}</span>
        <ChevronDown className="size-3.5 shrink-0 transition-transform duration-120 group-data-popup-open/model:rotate-180" />
      </DropdownMenuTrigger>
      <DropdownMenuContent side={side} sideOffset={6} className="max-h-80 w-80 overflow-y-auto">
        <DropdownMenuRadioGroup value={value.key} onValueChange={onChange}>
          {models.map((model) =>
            model.wallet && !walletConnected ? (
              <DropdownMenuItem
                key={model.key}
                className="items-start gap-2.5 rounded-[10px] py-2 pr-8 pl-2.5"
                onClick={() => openPrompt()}
              >
                <VendorAvatar
                  vendor={model.vendor}
                  name={model.label}
                  seed={model.key}
                  className="mt-0.5 size-6 rounded-md text-[11px]"
                />
                <span className="grid min-w-0 gap-0.5">
                  <span className="truncate text-[13.5px] font-medium">{model.label}</span>
                  <span className="text-muted-foreground text-xs leading-snug">
                    连接 TokenPay 钱包后可用（用你自己的 TokenDance 额度）
                  </span>
                </span>
                <Link2 className="text-muted-foreground mt-0.5 ml-auto size-4 shrink-0" />
              </DropdownMenuItem>
            ) : (
              <DropdownMenuRadioItem
                key={model.key}
                value={model.key}
                className="items-start gap-2.5 rounded-[10px] py-2 pr-8 pl-2.5"
              >
                <VendorAvatar
                  vendor={model.vendor}
                  name={model.label}
                  seed={model.key}
                  className="mt-0.5 size-6 rounded-md text-[11px]"
                />
                <span className="grid min-w-0 gap-0.5">
                  <span className="flex items-center gap-1.5">
                    <span className="truncate text-[13.5px] font-medium">{model.label}</span>
                    {model.wallet && (
                      <span className="text-muted-foreground inline-flex items-center gap-0.5 text-[10px]">
                        <Wallet className="size-3" />
                        钱包
                      </span>
                    )}
                  </span>
                  <span className="text-muted-foreground text-xs leading-snug">
                    {[
                      model.hint,
                      // 只展示 TokenDance 公开实时价目
                      modelPriceLabel(model),
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                </span>
              </DropdownMenuRadioItem>
            ),
          )}
        </DropdownMenuRadioGroup>
        {needConnect.length > 0 && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => openPrompt()}>
              <Link2 />
              连接 TokenPay 钱包，使用你自己的额度
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
