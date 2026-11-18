import type { RecordDto } from "@/api/conversation/type";
import type { TaskView } from "@/api/generation-task/type";
import type { WalletUsageItem } from "@/api/wallet";

/**
 * 钱包场景的「花了多少钱」：唯一来源是 TokenDance 的调用记录（用户在自己账号里真实付的钱）。
 * 平台积分体系对钱包用户是作废的——没有「平台积分价」，也没有「全局汇率」，
 * 任何由两者相乘得出的金额都是编造的（曾按 4 积分 × 0.01 显示 ¥0.04，真实收费是 ¥0.22）。
 */

/** 一条调用记录里的模型标识：优先用别名，上游没给别名才退回内部 id */
const slugOf = (item: WalletUsageItem) => item.model_slug || item.model_id;

/** 匹配的时间容差（毫秒）：上游记录时间可能略早于本站 submitted_at（时钟偏差 / 排队），放宽一点 */
const MATCH_SLACK_MS = 5 * 60_000;

/** 一条记录的格子：提交时间与截止时间，用来把调用记录对到这一格上 */
type CellWindow = { from: number; to: number };

const ms = (value: string | null | undefined) => (value ? Date.parse(value) : NaN);

/**
 * 把调用记录对到一条记录的每个格子上，返回这条记录的真实花费（元）。
 *
 * 匹配规则：同模型（usage.model_id === 本站模型的 upstream_model）、
 * 调用时间落在这一格的 [提交时间 - 容差, 截止时间] 内、取时间差最小且未被其它格子占用的一条。
 * 匹配不到（记录太老超出拉取范围、上游还没记上）返回 null——界面显示「—」，不猜。
 *
 * @param record 这条记录
 * @param tasks 它的任务快照（提交失败的格子不在其中）
 * @param usage 调用记录（newest first）
 * @param upstreamModel 所用模型的上游模型名；空串（模型已下线等）直接放弃匹配
 */
export function matchRecordCost(
  record: RecordDto,
  tasks: TaskView[],
  usage: WalletUsageItem[],
  upstreamModel: string,
): number | null {
  if (!upstreamModel || usage.length === 0) return null;
  // 一格一个时间窗；提交失败的格子没有任务，本来就没花钱，不参与匹配
  const windows = record.tasks
    .map((task) => (task ? tasks.find((item) => item.id === task.id) : undefined))
    .map(windowOf)
    .filter((cell): cell is CellWindow => cell !== null);
  if (windows.length === 0) return null;

  const taken = new Set<WalletUsageItem>();
  let total = 0;
  let matched = 0;
  for (const cell of windows) {
    const hit = pickUsage(usage, upstreamModel, cell, taken);
    if (!hit) continue;
    taken.add(hit);
    total += hit.cost_yuan;
    matched += 1;
  }
  // 一格格不上就不显示总额：半个金额比没有更容易误导
  return matched === windows.length ? total : null;
}

/** 一格的时间窗：从提交时间（排队中还没提交就取创建时间）到截止时间（没截止时间就放宽到提交后 30 分钟） */
function windowOf(task: TaskView | undefined): CellWindow | null {
  if (!task) return null;
  const from = ms(task.submitted_at) || ms(task.created_at);
  if (!Number.isFinite(from)) return null;
  const to = ms(task.finished_at) || ms(task.deadline_at) || from + 30 * 60_000;
  return { from: from - MATCH_SLACK_MS, to };
}

/** 在未占用的记录里挑时间最贴近这一格提交时间的一条 */
function pickUsage(
  usage: WalletUsageItem[],
  upstreamModel: string,
  cell: CellWindow | null,
  taken: Set<WalletUsageItem>,
): WalletUsageItem | undefined {
  if (!cell) return undefined;
  let best: WalletUsageItem | undefined;
  let bestGap = Infinity;
  for (const item of usage) {
    if (taken.has(item) || slugOf(item) !== upstreamModel) continue;
    const at = Date.parse(item.created_at);
    if (!Number.isFinite(at) || at < cell.from || at > cell.to) continue;
    const gap = Math.abs(at - cell.from);
    if (gap < bestGap) {
      best = item;
      bestGap = gap;
    }
  }
  return best;
}

/**
 * 金额文案：整数部分不带多余小数，小数部分按量级保留位数，前面加 ¥。
 * 预估场景由调用方自己加「≈」——这里是钱，不是估算值。
 */
export function formatYuan(yuan: number): string {
  if (yuan <= 0) return "¥0";
  if (yuan < 0.01) return `¥${yuan.toFixed(4).replace(/0+$/, "").replace(/\.$/, "")}`;
  if (yuan < 1) return `¥${yuan.toFixed(2)}`;
  if (yuan < 100) return `¥${yuan.toFixed(1)}`;
  return `¥${Math.round(yuan)}`;
}
