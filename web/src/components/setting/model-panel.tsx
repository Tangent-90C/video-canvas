import type { ReactNode } from "react";
import { ChevronDown } from "lucide-react";

import { buttonVariants } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { useSettingsStore } from "@/store";

import { SettingRow } from "./setting-controls";

/** 一种节点可挑的模型，形状和输入框里的下拉一致 */
export type SettingModelOption = {
  id: string;
  label: string;
  priceLabel?: string;
  hint?: string;
  wallet?: boolean;
};

/** 按节点种类分好的模型清单，由调用方喂进来 */
export type SettingModelGroup = {
  /** 种类标识，也是 defaultModels 里的键 */
  kind: string;
  /** 种类名，例如「图片」 */
  label: string;
  icon?: ReactNode;
  models: readonly SettingModelOption[];
};

type ModelPanelProps = {
  groups: readonly SettingModelGroup[];
};

/** 模型设置：给每种节点定一个新建时默认选中的模型 */
export function ModelPanel({ groups }: ModelPanelProps) {
  const defaultModels = useSettingsStore((state) => state.defaultModels);
  const setDefaultModel = useSettingsStore((state) => state.setDefaultModel);

  return (
    <>
      {groups.map((group) => {
        const current =
          group.models.find((item) => item.id === defaultModels[group.kind]) ?? group.models[0];

        return (
          <SettingRow
            key={group.kind}
            title={`${group.label}节点`}
            hint={current.priceLabel ?? "按 TokenDance 实际账单"}
          >
            <DropdownMenu>
              <DropdownMenuTrigger
                className={cn(
                  buttonVariants({ variant: "outline", size: "sm" }),
                  "min-w-0 max-w-full gap-2",
                )}
                aria-label={`选择${group.label}节点的默认模型`}
              >
                {group.icon}
                <span className="min-w-0 truncate">{current.label}</span>
                <ChevronDown className="opacity-60" />
              </DropdownMenuTrigger>
              <DropdownMenuContent className="w-60" align="end" sideOffset={6}>
                <DropdownMenuGroup>
                  <DropdownMenuRadioGroup
                    value={current.id}
                    onValueChange={(next) => setDefaultModel(group.kind, next as string)}
                  >
                    {group.models.map((item) => (
                      <DropdownMenuRadioItem key={item.id} value={item.id}>
                        <span className="flex min-w-0 flex-1 flex-col">
                          <span className="truncate">{item.label}</span>
                          {item.hint && (
                            <span className="text-muted-foreground truncate text-xs">
                              {item.hint}
                            </span>
                          )}
                        </span>
                        <span className="text-muted-foreground ml-auto max-w-36 truncate text-right text-xs">
                          {item.priceLabel ?? "按 TokenDance 实际账单"}
                        </span>
                      </DropdownMenuRadioItem>
                    ))}
                  </DropdownMenuRadioGroup>
                </DropdownMenuGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          </SettingRow>
        );
      })}
    </>
  );
}
