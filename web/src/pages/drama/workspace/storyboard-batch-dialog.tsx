import { useState } from "react";
import { Layers, Loader2 } from "lucide-react";
import { toast } from "sonner";

import { submitJob } from "@/api/drama";
import { dramaErrorMessage } from "@/api/drama/errors";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useDramaJobsStore } from "@/store/drama";
import {
  MAX_STORYBOARD_BATCH,
  selectByRange,
  storyboardBatchSubmit,
  storyboardOptions,
} from "@/utils/drama/episodes";

import { useWorkspace } from "./context";
import { EpisodeGrid, RangeSelect } from "./episode-pick";

/**
 * 对话框主体：每次打开重新挂载，预选当前这一集。
 * 每集各提交一个分镜任务、同时发出（不同集的分镜后端可以并行，排队并发由后台统一限制）；
 * 部分失败时成功的照常进行，失败的集写明原因。
 */
export function StoryboardBatchBody({
  initial,
  maxSeconds,
  onClose,
}: {
  /** 预先选中的集（片场当前集），不能选时忽略 */
  initial: number;
  /** 视频模型的片段时长上限，和单集「生成脚本」一致 */
  maxSeconds: number;
  onClose: () => void;
}) {
  const { drama, jobs, refresh } = useWorkspace();
  const options = storyboardOptions(drama.episodes, jobs);
  const available = options.filter((option) => !option.disabled).map((option) => option.no);
  const [selected, setSelected] = useState<number[]>(() =>
    available.includes(initial) ? [initial] : [],
  );
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = storyboardBatchSubmit(selected);

  const toggle = (no: number) =>
    setSelected((prev) =>
      prev.includes(no) ? prev.filter((n) => n !== no) : [...prev, no].sort((a, b) => a - b),
    );

  const start = async () => {
    if (!submit.ok || submitting) return;
    setSubmitting(true);
    setError(null);
    const results = await Promise.allSettled(
      selected.map((no) => submitJob(drama.id, { step: "storyboard", episodes: [no], maxSeconds })),
    );
    const failed: string[] = [];
    let started = 0;
    results.forEach((result, index) => {
      if (result.status === "fulfilled") {
        useDramaJobsStore.getState().applyJob(drama.id, result.value);
        started += 1;
      } else {
        failed.push(
          `第 ${selected[index]} 集：${dramaErrorMessage(result.reason, "提交失败，请稍后重试")}`,
        );
      }
    });
    void refresh();
    if (started === 0) {
      setError(failed.join("；"));
      setSubmitting(false);
      return;
    }
    toast.success(`已提交 ${started} 集分镜脚本`);
    if (failed.length > 0) toast.warning(`有 ${failed.length} 集没能提交：${failed[0]}`);
    onClose();
  };

  return (
    <>
      <DialogHeader>
        <DialogTitle>批量生成分镜脚本</DialogTitle>
      </DialogHeader>

      <section className="grid gap-2">
        <div className="flex items-center gap-2 text-sm">
          <b>选择集数</b>
          <span className="text-muted-foreground text-xs tabular-nums">
            已选 {selected.length} 集
          </span>
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto"
            disabled={available.length === 0}
            onClick={() => setSelected(available.slice(0, MAX_STORYBOARD_BATCH))}
          >
            全选可用
          </Button>
          <Button
            variant="ghost"
            size="sm"
            disabled={selected.length === 0}
            onClick={() => setSelected([])}
          >
            清空
          </Button>
        </div>
        <RangeSelect
          max={options.at(-1)?.no ?? 1}
          onSelect={(from, to) => setSelected(selectByRange(options, from, to))}
        />
        <EpisodeGrid options={options} selected={selected} onToggle={toggle} />
      </section>

      <div className="bg-muted/50 flex items-center gap-2 rounded-lg px-3 py-2 text-sm">
        <div className="min-w-0 flex-1">
          生成 <b className="tabular-nums">{selected.length}</b> 集，同时开始
        </div>
        <div className="shrink-0 text-xs tabular-nums">费用按 TokenDance 实际账单</div>
      </div>

      {error && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}

      <DialogFooter>
        <Button variant="ghost" onClick={onClose}>
          取消
        </Button>
        <Button disabled={!submit.ok || submitting} onClick={() => void start()}>
          {submitting && <Loader2 className="animate-spin" />}
          {submit.label}
        </Button>
      </DialogFooter>
    </>
  );
}

/**
 * 批量生成分镜脚本对话框：常驻挂载，open 开关。
 * @param session 每次打开换一个值，主体按它重新挂载、重置选择；关闭时主体不卸载，留给退出动画
 */
export function StoryboardBatchDialog({
  open,
  onOpenChange,
  session,
  initial,
  maxSeconds,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  session: number;
  initial: number;
  maxSeconds: number;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <StoryboardBatchBody
          key={session}
          initial={initial}
          maxSeconds={maxSeconds}
          onClose={() => onOpenChange(false)}
        />
      </DialogContent>
    </Dialog>
  );
}

/**
 * 片场右上角的「批量生成脚本」：按钮和弹窗放在一起，空状态的工具条和片场顶栏共用。
 * @param current 当前看的集，打开时预选（不能选时忽略）
 * @param maxSeconds 视频模型的片段时长上限
 */
export function StoryboardBatchButton({
  current,
  maxSeconds,
}: {
  current: number;
  maxSeconds: number;
}) {
  const [open, setOpen] = useState(false);
  const [session, setSession] = useState(0);
  const openDialog = () => {
    setSession((n) => n + 1);
    setOpen(true);
  };
  return (
    <>
      <Button variant="outline" size="sm" onClick={openDialog}>
        <Layers />
        批量生成脚本
      </Button>
      <StoryboardBatchDialog
        open={open}
        onOpenChange={setOpen}
        session={session}
        initial={current}
        maxSeconds={maxSeconds}
      />
    </>
  );
}
