import { useCallback, useMemo, useRef, useState, type ReactNode } from "react";
import { motion } from "motion/react";
import { ArrowUp, ChevronDown, Loader2, Maximize2, Wallet } from "lucide-react";

import type { Capabilities, UpstreamPricing } from "@/api/model/type";
import { VendorAvatar } from "@/components/admin-ui/vendor-avatar";
import { useWalletPriceLabel } from "@/hooks/use-wallet-cost";
import { useWalletStore } from "@/store/wallet";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { TAP } from "@/lib/motion";
import { cn } from "@/lib/utils";
import { canvasDialogGuards } from "@/utils/canvas/dialog-guards";
import { promptPresets, promptTextLength } from "@/utils/canvas/prompt-tokens";

import {
  PromptEditor,
  PromptRefsProvider,
  useRefHighlight,
  type PresetChipTarget,
  type PromptEditorHandle,
  type PromptMentionSource,
} from "./prompt-mention";

/** 提示词最多多少字，和后端校验一致 */
export const PROMPT_MAX_LENGTH = 16000;
/** 字数到这个比例开始提醒 */
const PROMPT_WARN_RATIO = 0.94;

/** 一个可选模型：名字之外还带 TokenDance 公开价目 */
export type NodeModelOption = {
  /** 模型标识，写回节点数据的就是它 */
  id: string;
  /** 模型显示名 */
  label: string;
  /** TokenDance 公开价目文案 */
  priceLabel?: string;
  /** 下拉里的一行小字，说明擅长什么 */
  hint?: string;
  /** 厂商 slug，用来显示 logo；没有时回退首字头像 */
  vendor?: string;
  /** 展示标签 */
  tags?: readonly string[];
  /** TokenPay 钱包模型：用用户自己的 TokenDance 额度支付 */
  wallet?: boolean;
  /** 上游模型名；钱包模型用它匹配 TokenDance 调用记录，算真实花费 */
  upstream_model?: string;
  /** 原始能力：需要按参数重新报价时保留 */
  capabilities?: Capabilities;
  /** 后端模型种类 */
  kind?: string;
  /** 钱包模型的上游公开价目 */
  upstream_pricing?: UpstreamPricing;
};

/** 面板底栏上的纯文字按钮样式（无描边，只有悬停底色），模型、参数摘要这类按钮共用 */
export const PANEL_CHIP_CLASS = cn(
  "nodrag inline-flex h-8 min-w-0 shrink items-center gap-1 rounded-md px-2.5 text-[13px] font-medium whitespace-nowrap",
  "transition-[background-color,color] hover:bg-chrome-hover data-popup-open:bg-chrome-hover",
  "focus-visible:ring-node-ring/60 outline-none focus-visible:ring-2",
  "disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-3 [&_svg]:shrink-0",
);

/** 发送键旁边的第二种生成（如角色卡的「生成（含音色）」）：和发送键共用忙碌状态 */
export type SecondarySubmit = {
  label: string;
  /** 此刻不能点的原因；可以点为 null */
  blockedReason: string | null;
  onSubmit: () => void;
};

type NodePromptInputProps = {
  /** 提示词文本，@ 进来的素材写成 `@[名字](节点 id)` */
  value: string;
  onValueChange: (value: string) => void;
  /** 空输入时的提示 */
  placeholder?: string;
  /** 可选模型清单 */
  models: readonly NodeModelOption[];
  /** 当前选中的模型 id */
  modelId: string;
  onModelChange: (id: string) => void;
  /** 正在生成：发送按钮转圈并锁住 */
  running?: boolean;
  /** 不给就只存提示词，不跑生成 */
  onSubmit?: () => void;
  /** 没有 onSubmit 时，鼠标停在发送键上要给的说法 */
  submitHint?: string;
  /**
   * 由调用方接管「能不能发」的判断（比如按 schema 校验必填项）。
   * 不给就沿用默认：有 onSubmit、没在跑、提示词非空。
   */
  canSubmit?: boolean;
  /** 接管发送键的悬停说明，通常写清「为什么现在发不了」 */
  hint?: string;
  /** 提交请求在路上：发送键转圈，但和「正在生成」区分开 */
  submitting?: boolean;
  /** 模型触发器上显示的字，覆盖清单里的名字；清单为空或模型下线时用 */
  modelLabel?: string;
  /** 当前模型有问题（已下线等），触发器用警示色 */
  modelInvalid?: boolean;
  /** 生成数量（一次提交拆成几个任务） */
  count?: number;
  /** 提示词框禁用（比如提示词由上游连线提供），placeholder 会换成 promptNote */
  promptDisabled?: boolean;
  promptNote?: string;
  /** 完全不要提示词框（该模型没有 prompt 字段） */
  hidePrompt?: boolean;
  /** 当前模型的提示词字数上限，预设选择器用它提示放不下的预设 */
  promptMaxLength?: number;
  /** 面板最上面一行，放生成方式 Tabs */
  header?: ReactNode;
  /**
   * 提示词框上方的插槽，放引用条；没有 header 时它顶到第一行，和放大按钮并排。
   * 「编辑提示词」弹窗里在编辑区上面再渲染一份同样的（同一个元素、同一个上下文），没有它弹窗里也没有
   */
  children?: ReactNode;
  /** 提示词里 @ 素材要的数据；不给就不能 @ */
  mention?: PromptMentionSource;
  /** 工具栏上方的一行提示，比如提交被拒的原因 */
  notice?: { tone: "error" | "info"; text: string } | null;
  /** 底栏里模型选择后面的插槽，放参数摘要、批量 */
  toolbarExtra?: ReactNode;
  /** 发送键旁边的第二种生成；不给就只有发送键 */
  secondarySubmit?: SecondarySubmit;
  /** 面板宽度（屏幕像素）；不给就 480 */
  width?: number;
  className?: string;
};

/** 底栏（提交提示 + 模型 / 参数 / 公开价目 / 发送）要的数据：面板和放大编辑弹窗共用同一份 */
type PromptFooterProps = {
  /** 工具栏上方的一行提示，比如提交被拒的原因 */
  notice?: { tone: "error" | "info"; text: string } | null;
  /** 可选模型清单 */
  models: readonly NodeModelOption[];
  /** 当前选中的模型，清单为空时是 undefined */
  model: NodeModelOption | undefined;
  onModelChange: (id: string) => void;
  /** 模型触发器上显示的字，覆盖清单里的名字 */
  modelLabel?: string;
  /** 当前模型有问题，触发器用警示色 */
  modelInvalid?: boolean;
  /** 模型选择后面的插槽，放参数摘要、批量 */
  toolbarExtra?: ReactNode;
  /** 价格与发送键的悬停说明 */
  creditsTitle: string;
  /** 钱包模型的公开价目文案；普通渠道模型不传 */
  walletEstimate?: string | null;
  /** 选择器里有钱包模型，但用户还没连接 TokenPay */
  walletRequired?: boolean;
  /** 打开 TokenPay 连接弹窗 */
  onConnectWallet?: () => void;
  /** 发送键不可点：不能提交，或正在忙 */
  blocked: boolean;
  /** 提交在路上或正在生成：发送键转圈 */
  busy?: boolean;
  /** 点发送键 */
  onSubmit: () => void;
  /** 发送键旁边的第二种生成 */
  secondarySubmit?: SecondarySubmit;
};

/**
 * 生成面板的底栏：提交被拒的提示行，其下是模型下拉、参数摘要 / 批量插槽、公开价目和圆形发送键。
 * 面板和「编辑提示词」弹窗各渲染一次，吃同一份 props，所以在弹窗里改模型、参数，面板里同步变。
 */
function PromptFooter({
  notice,
  models,
  model,
  onModelChange,
  modelLabel,
  modelInvalid,
  toolbarExtra,
  creditsTitle,
  walletEstimate,
  walletRequired,
  onConnectWallet,
  blocked,
  busy,
  onSubmit,
  secondarySubmit,
}: PromptFooterProps) {
  return (
    <div className="flex min-w-0 flex-col gap-3">
      {walletRequired && onConnectWallet && (
        <button
          type="button"
          onClick={onConnectWallet}
          className="text-muted-foreground hover:bg-chrome-hover hover:text-foreground focus-visible:ring-node-ring/60 flex items-center gap-1.5 rounded-lg px-2 py-1 text-left text-xs outline-none transition-colors focus-visible:ring-2"
        >
          <Wallet className="size-3.5" />
          连接 TokenPay 钱包后生成
        </button>
      )}
      {notice && (
        <p
          role={notice.tone === "error" ? "alert" : undefined}
          className={cn(
            "rounded-lg px-2.5 py-1.5 text-xs",
            notice.tone === "error"
              ? "bg-destructive/10 text-destructive"
              : "bg-muted text-muted-foreground",
          )}
        >
          {notice.text}
        </p>
      )}

      <div className="flex min-w-0 items-center gap-2">
        <DropdownMenu modal={false}>
          <DropdownMenuTrigger
            className={cn(PANEL_CHIP_CLASS, modelInvalid && "text-destructive")}
            aria-label="选择模型"
          >
            <span className="min-w-0 truncate">{modelLabel ?? model?.label ?? "加载模型…"}</span>
            <ChevronDown className="opacity-50" />
          </DropdownMenuTrigger>
          <DropdownMenuContent
            className="max-h-80 w-72 overflow-y-auto"
            align="start"
            sideOffset={8}
          >
            <DropdownMenuGroup>
              {/* GroupLabel 必须待在 Group 里，否则 Base UI 会抛 MenuGroupContext 缺失 */}
              <DropdownMenuLabel>选择模型</DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuRadioGroup
                value={model?.id ?? ""}
                onValueChange={(next) => onModelChange(next as string)}
              >
                {models.map((item) => (
                  <DropdownMenuRadioItem key={item.id} value={item.id}>
                    <VendorAvatar
                      vendor={item.vendor}
                      name={item.label}
                      seed={item.id}
                      className="size-7 rounded-md text-xs"
                    />
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="flex items-center gap-1.5">
                        <span className="truncate">{item.label}</span>
                        {item.tags?.map((tag) => (
                          <span
                            key={tag}
                            className="bg-muted text-muted-foreground shrink-0 rounded px-1 py-px text-[10px] leading-4"
                          >
                            {tag}
                          </span>
                        ))}
                      </span>
                      {item.hint && (
                        <span className="text-muted-foreground truncate text-xs">{item.hint}</span>
                      )}
                    </span>
                    <span className="text-muted-foreground ml-auto max-w-44 shrink-0 truncate text-right text-xs">
                      {item.priceLabel ?? "按 TokenDance 实际账单"}
                    </span>
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            </DropdownMenuGroup>
          </DropdownMenuContent>
        </DropdownMenu>

        {toolbarExtra}

        {/* TokenPay 价格是灰色小字，发送键是独立的实心圆：模型价目一眼看到 */}
        <span className="ml-auto flex shrink-0 items-center gap-3" title={creditsTitle}>
          {/* 第二种生成（角色卡的「含音色」）排在前面，和发送键一样是「费用口径 + 实心键」 */}
          {secondarySubmit && (
            <SubmitPill
              label={secondarySubmit.label}
              walletEstimate={walletEstimate}
              title={
                busy
                  ? "生成中"
                  : // 钱包任务按 TokenDance 实际账单计费，不展示历史积分金额
                    walletEstimate
                    ? `${secondarySubmit.blockedReason ?? "生成"} · ${walletEstimate}`
                    : (secondarySubmit.blockedReason ?? "按 TokenDance 实际账单")
              }
              blocked={busy || !!secondarySubmit.blockedReason}
              busy={busy}
              onClick={secondarySubmit.onSubmit}
            />
          )}
          {walletEstimate ? (
            <span className="text-muted-foreground flex items-center gap-1 text-[13px] font-medium tabular-nums">
              <Wallet className="size-3.5" />
              {walletEstimate}
            </span>
          ) : (
            <span className="text-muted-foreground flex items-center gap-1 text-[13px] font-medium tabular-nums">
              <Wallet className="size-3.5" />
              按 TokenDance 账单
            </span>
          )}
          <motion.button
            type="button"
            whileTap={blocked ? undefined : { scale: 0.92 }}
            whileHover={blocked ? undefined : { scale: 1.05 }}
            aria-label={busy ? "生成中" : "开始生成"}
            disabled={blocked}
            onClick={onSubmit}
            className={cn(
              "bg-foreground text-background focus-visible:ring-node-ring/60 grid size-8 place-items-center rounded-full outline-none transition-colors focus-visible:ring-2 disabled:pointer-events-none",
              blocked && "bg-foreground/35",
            )}
          >
            {busy ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <ArrowUp className="size-4 stroke-[2.5]" />
            )}
          </motion.button>
        </span>
      </div>
    </div>
  );
}

/** 第二种生成：灰色公开价目 + 带文字的实心键，长相跟着发送键走 */
function SubmitPill({
  label,
  title,
  blocked,
  busy,
  onClick,
  walletEstimate,
}: {
  label: string;
  /** 悬停说明：不能点的原因或公开价目 */
  title: string;
  blocked: boolean;
  busy?: boolean;
  onClick: () => void;
  /** TokenDance 公开价目文案 */
  walletEstimate?: string | null;
}) {
  return (
    // 不能点的按钮收不到鼠标事件，悬停说明挂在外层
    <span className="flex items-center gap-3" title={title}>
      {walletEstimate ? (
        <span className="text-muted-foreground flex items-center gap-1 text-[13px] font-medium tabular-nums">
          <Wallet className="size-3.5" />
          {walletEstimate}
        </span>
      ) : (
        <span className="text-muted-foreground flex items-center gap-1 text-[13px] font-medium tabular-nums">
          <Wallet className="size-3.5" />
          按 TokenDance 账单
        </span>
      )}
      <motion.button
        type="button"
        whileTap={blocked ? undefined : { scale: 0.92 }}
        whileHover={blocked ? undefined : { scale: 1.05 }}
        aria-label={busy ? "生成中" : label}
        disabled={blocked}
        onClick={onClick}
        className={cn(
          "bg-foreground text-background focus-visible:ring-node-ring/60 inline-flex h-8 items-center gap-1 rounded-full pr-2 pl-3 text-[13px] font-medium whitespace-nowrap outline-none transition-colors focus-visible:ring-2 disabled:pointer-events-none",
          blocked && "bg-foreground/35",
        )}
      >
        {label}
        {busy ? (
          <Loader2 className="size-4 animate-spin" />
        ) : (
          <ArrowUp className="size-4 stroke-[2.5]" />
        )}
      </motion.button>
    </span>
  );
}

/**
 * 浮在节点下方的生成面板（设计稿 6.4）：
 * 生成方式 Tabs → 引用条 → 提示词（可 @ 素材）→ 字数 → 底栏（模型、参数、批量、公开价目 + 发送）。
 * 右上角可以把提示词放大到对话框里写，对话框里带引用条和同一条底栏（设计稿 10.2）。
 * 只管长相和交互，数据都由调用方决定。
 */
export function NodePromptInput({
  value,
  onValueChange,
  placeholder = "写下你想要的内容",
  models,
  modelId,
  onModelChange,
  running,
  onSubmit,
  submitHint = "这类节点还没接入生成服务",
  canSubmit: canSubmitOverride,
  hint: hintOverride,
  submitting,
  modelLabel,
  modelInvalid,
  promptDisabled,
  promptNote,
  hidePrompt,
  promptMaxLength,
  header,
  children,
  mention,
  notice,
  toolbarExtra,
  secondarySubmit,
  width = 480,
  className,
}: NodePromptInputProps) {
  const [expanded, setExpanded] = useState(false);
  const editorRef = useRef<PromptEditorHandle>(null);
  /** 放大编辑弹窗里的编辑器；弹窗关了就是 null */
  const dialogEditorRef = useRef<PromptEditorHandle>(null);
  const [highlight, setHighlight] = useRefHighlight();
  const [presetChip, setPresetChip] = useState<PresetChipTarget | null>(null);
  const linkedIds = mention?.linkedIds;
  const activeEditor = useCallback(
    () => (expanded ? dialogEditorRef.current : null) ?? editorRef.current,
    [expanded],
  );
  const refsValue = useMemo(
    () => ({
      linkedIds: linkedIds ?? new Set<string>(),
      highlight,
      setHighlight,
      // 弹窗打开时面板被盖住，引用、预设都落到弹窗里的编辑器；弹窗关了回到面板里的
      insertRef: (source: Parameters<PromptEditorHandle["insertRef"]>[0]) =>
        activeEditor()?.insertRef(source),
      presets: {
        selected: promptPresets(value).map(({ kind, id }) => ({ kind, id })),
        maxLength: promptMaxLength,
        apply: (...args: Parameters<PromptEditorHandle["applyPreset"]>) =>
          activeEditor()?.applyPreset(...args),
        focusEditor: () => activeEditor()?.focus(),
        chip: presetChip,
        setChip: setPresetChip,
      },
    }),
    [activeEditor, highlight, linkedIds, presetChip, promptMaxLength, setHighlight, value],
  );
  const model = models.find((item) => item.id === modelId) ?? models[0];
  const canSubmit = canSubmitOverride ?? (!!onSubmit && !running && value.trim().length > 0);
  const busy = running || submitting;
  const blocked = !canSubmit || busy;
  // 钱包模型只展示 TokenDance 公开价目；不再显示平台积分或历史均价。
  const walletEstimate = useWalletPriceLabel(model);
  const walletConnected = useWalletStore((state) => !!state.view?.connected);
  const openWalletPrompt = useWalletStore((state) => state.openPrompt);

  const submit = () => {
    if (!blocked) onSubmit?.();
  };

  // 按钮灰着的时候得说清为什么，不然只剩一个点不动的圈；
  // disabled 的按钮本身收不到鼠标事件，提示挂在外面那层上
  const hint =
    hintOverride ??
    (running ? "生成中" : !onSubmit ? submitHint : value.trim() ? "开始生成" : "先写点提示词");
  const creditsTitle = walletEstimate
    ? `${hint} · ${walletEstimate}（TokenDance 公开价目）`
    : `${hint} · 按 TokenDance 实际账单`;
  const promptPlaceholder = promptDisabled && promptNote ? promptNote : placeholder;
  const length = promptTextLength(value);
  const nearLimit = length > PROMPT_MAX_LENGTH * PROMPT_WARN_RATIO;
  const footer: PromptFooterProps = {
    notice,
    models,
    model,
    onModelChange,
    modelLabel,
    modelInvalid,
    toolbarExtra,
    creditsTitle,
    walletEstimate,
    walletRequired: models.some((item) => item.wallet) && !walletConnected,
    onConnectWallet: openWalletPrompt,
    blocked: !!blocked,
    busy,
    onSubmit: submit,
    secondarySubmit,
  };
  // 没有生成方式 Tabs 时，引用条顶到第一行，和放大按钮并排（设计稿 6.7）
  const top = header ?? children;
  const below = header ? children : null;

  return (
    // nodrag 让框里能正常选字、点按钮，不会顺手把画布拖走
    <PromptRefsProvider value={refsValue}>
      <div
        style={{ width }}
        className={cn(
          "nodrag nopan bg-panel text-popover-foreground ring-foreground/5 flex flex-col gap-3 rounded-2xl p-4 text-left shadow-2xl ring-1",
          // 放大编辑时面板藏起来（弹窗是 Portal 渲染到 body 的，不受影响）；用 invisible 保留占位，布局不跳
          expanded && "invisible",
          className,
        )}
      >
        {(top || !hidePrompt) && (
          <div className="flex min-w-0 items-start gap-2">
            <div className="min-w-0 flex-1">{top}</div>
            {!hidePrompt && (
              <motion.button
                type="button"
                whileTap={TAP}
                aria-label="放大编辑提示词"
                title="放大编辑"
                disabled={promptDisabled}
                onClick={() => setExpanded(true)}
                className="text-muted-foreground hover:bg-chrome-hover hover:text-foreground focus-visible:ring-node-ring/60 grid size-7 shrink-0 place-items-center rounded-md outline-none transition-colors focus-visible:ring-2 disabled:opacity-40"
              >
                <Maximize2 className="size-3.5" />
              </motion.button>
            )}
          </div>
        )}

        {below}

        {!hidePrompt && (
          <div className="flex flex-col gap-1">
            <PromptEditor
              ref={editorRef}
              value={value}
              onValueChange={onValueChange}
              placeholder={promptPlaceholder}
              disabled={promptDisabled}
              onSubmit={submit}
              mention={mention}
            />
            {/* 没写字时不显示，但占着位置，免得一输入面板就往下顶 */}
            <span
              className={cn(
                "self-end font-mono text-xs tabular-nums",
                nearLimit ? "text-status-warning" : "text-muted-foreground/60",
                length === 0 && "invisible",
              )}
            >
              {length}/{PROMPT_MAX_LENGTH}
            </span>
          </div>
        )}

        <PromptFooter {...footer} />

        <Dialog open={expanded} onOpenChange={setExpanded}>
          <DialogContent className="sm:max-w-4xl" {...canvasDialogGuards}>
            {/* 不显示标题，但 Radix 要求弹窗有标题，留给读屏软件 */}
            <DialogTitle className="sr-only">编辑提示词</DialogTitle>
            {/* 引用条和面板里是同一份：同一个元素、同一个上下文，点击插入落到下面这个编辑器 */}
            {children}
            {/* 编辑区和弹窗同一个底色，不再单独垫一块 */}
            <PromptEditor
              ref={dialogEditorRef}
              large
              autoFocus
              value={value}
              onValueChange={onValueChange}
              placeholder={promptPlaceholder}
              disabled={promptDisabled}
              mention={mention}
              onSubmit={() => {
                setExpanded(false);
                submit();
              }}
            />
            <div className="text-muted-foreground flex items-center justify-end text-xs">
              <span className={cn("font-mono tabular-nums", nearLimit && "text-status-warning")}>
                {length}/{PROMPT_MAX_LENGTH}
              </span>
            </div>
            {/* 和面板同一条底栏：模型、参数、公开价目、发送都是同一份状态；点发送与 ⌘↵ 一致，先收起再提交 */}
            <PromptFooter
              {...footer}
              onSubmit={() => {
                setExpanded(false);
                submit();
              }}
              secondarySubmit={
                secondarySubmit && {
                  ...secondarySubmit,
                  onSubmit: () => {
                    setExpanded(false);
                    secondarySubmit.onSubmit();
                  },
                }
              }
            />
          </DialogContent>
        </Dialog>
      </div>
    </PromptRefsProvider>
  );
}
