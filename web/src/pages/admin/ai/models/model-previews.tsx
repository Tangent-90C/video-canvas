import { useEffect, useState, type ReactNode } from "react";
import { Eye } from "lucide-react";

import { listModels } from "@/api/admin/ai";
import type { ModelListItem } from "@/api/admin/ai/type.d";
import { Tag } from "@/components/admin-ui/tag";
import { VendorAvatar } from "@/components/admin-ui/vendor-avatar";
import { cn } from "@/lib/utils";
import { MODEL_KIND_LABEL } from "@/utils/admin/model-body";

/** 右侧预览栏的小标题 + 底部说明 */
export function PreviewFrame({
  title,
  note,
  children,
}: {
  title: string;
  note?: ReactNode;
  children: ReactNode;
}) {
  return (
    <>
      <div className="text-muted-foreground mb-3 flex items-center gap-2 text-xs font-medium">
        <Eye className="size-3.5" />
        {title}
      </div>
      {children}
      {note && <p className="text-muted-foreground mt-3 text-xs leading-relaxed">{note}</p>}
    </>
  );
}

function PickerRow({
  name,
  seed,
  vendor,
  tags,
  price,
  hint,
  active,
}: {
  name: string;
  seed: string;
  vendor?: string;
  tags?: readonly string[];
  /** 费用口径文案，如「按 TokenDance 实际账单」 */
  price?: string;
  hint?: string;
  active?: boolean;
}) {
  return (
    <div
      className={cn(
        "flex gap-3 rounded-lg p-2.5",
        active ? "bg-accent ring-foreground/20 ring-1" : "opacity-45",
      )}
    >
      <VendorAvatar vendor={vendor} name={name} seed={seed} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <span className="truncate text-sm font-medium">{name || "未命名模型"}</span>
          {tags?.map((tag) => (
            <Tag key={tag} tone="info" className="px-1 py-0 text-[10px]">
              {tag}
            </Tag>
          ))}
          {price && (
            <span className="text-muted-foreground ml-auto shrink-0 text-xs tabular-nums">
              {price}
            </span>
          )}
        </div>
        {hint && <p className="text-muted-foreground mt-1 line-clamp-2 text-xs">{hint}</p>}
      </div>
    </div>
  );
}

/** 基本信息页签的预览：创作端的模型选择器，高亮当前模型，灰色是同类已上架的模型 */
export function PickerPreview({
  modelKey,
  label,
  kind,
  vendor,
  tags,
  price,
  hint,
}: {
  modelKey: string;
  label: string;
  kind: string;
  vendor: string;
  tags: readonly string[];
  price: string;
  hint: string;
}) {
  // 灰色的“同类已上架的模型”：向后端要同类型在线的前 3 个，排除自己后留 2 个
  const [others, setOthers] = useState<ModelListItem[]>([]);
  useEffect(() => {
    if (!kind) return setOthers([]);
    let cancelled = false;
    listModels({ kind, status: "online", page: 1, page_size: 3 })
      .then((page) => {
        if (!cancelled) setOthers(page.list.filter((item) => item.key !== modelKey).slice(0, 2));
      })
      .catch(() => !cancelled && setOthers([]));
    return () => {
      cancelled = true;
    };
  }, [kind, modelKey]);
  return (
    <PreviewFrame title="创作端预览 · 模型选择器">
      <div className="bg-popover rounded-xl border p-2 shadow-lg">
        <div className="text-muted-foreground px-2 pt-1 pb-2 text-xs">
          {MODEL_KIND_LABEL[kind] ?? kind}模型
        </div>
        <PickerRow
          active
          name={label}
          seed={modelKey}
          vendor={vendor}
          tags={tags}
          price={price}
          hint={hint}
        />
        {others.map((item) => (
          <PickerRow
            key={item.key}
            name={item.label || item.name || item.key}
            seed={item.key}
            vendor={item.vendor}
            tags={item.tags}
          />
        ))}
      </div>
    </PreviewFrame>
  );
}
