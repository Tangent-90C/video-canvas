import type { Capabilities } from "@/api/model/type";

/** 取整数参数：数字或数字字符串都认（前端可能把枚举值存成字符串），取不到为 0 */
function intParam(params: Record<string, unknown>, name: string) {
  const raw = params[name];
  const n = typeof raw === "number" ? raw : typeof raw === "string" ? Number(raw.trim()) : NaN;
  return Number.isFinite(n) ? Math.trunc(n) : 0;
}

/** 生成数量参数的名字；没有 fanout 参数为 undefined */
export const fanoutParam = (caps: Capabilities | undefined) =>
  Object.entries(caps?.params ?? {}).find(([, field]) => field.fanout)?.[0];

/** 生成数量：一次提交拆成几个任务；没有 fanout 参数为 1 */
export function fanoutCount(caps: Capabilities | undefined, params: Record<string, unknown>) {
  const name = fanoutParam(caps);
  if (!name) return 1;
  const n = intParam(params, name);
  return n > 0 ? n : 1;
}
