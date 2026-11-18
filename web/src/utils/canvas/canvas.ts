import type { HandleType } from "@xyflow/react";

import {
  DOWNSTREAM_KINDS,
  isRemoteModelKind,
  MODEL_LIBRARY,
  NODE_LIBRARY,
} from "@/constants/canvas";
import type { ModelOption, NodeKind } from "@/types";

/** 从某个节点的某一端拉线时，允许新建的种类；从 target 端拉线要反查谁能生成它 */
export function getAllowedKinds(kind: NodeKind, handleType: HandleType): NodeKind[] {
  if (handleType === "source") return DOWNSTREAM_KINDS[kind];

  return NODE_LIBRARY.filter((meta) => DOWNSTREAM_KINDS[meta.kind].includes(kind)).map(
    (meta) => meta.kind,
  );
}

/**
 * 从某个节点的某一端拉出来的线，能不能接到另一个种类的节点上。
 * 从 source 端拉出时对方是下游，从 target 端拉出时对方得是能生成自己的上游。
 * 组节点没有种类（kind 为 undefined），谁也接不上，不能因此抛异常。
 */
export function canConnectKinds(
  fromKind: NodeKind,
  handleType: HandleType,
  toKind: NodeKind,
): boolean {
  return handleType === "source"
    ? (DOWNSTREAM_KINDS[fromKind]?.includes(toKind) ?? false)
    : (DOWNSTREAM_KINDS[toKind]?.includes(fromKind) ?? false);
}

/** 取某种类可选的模型 */
export function getModels(kind: NodeKind): readonly ModelOption[] {
  return MODEL_LIBRARY[kind];
}

/**
 * 某种类真正能挑的模型：用户侧只认服务端下发清单；导演台、角色卡等不调模型的节点没有模型。
 * @param kind 节点种类
 * @param remote 服务端下发的模型选项
 */
export function getModelOptions(
  kind: NodeKind,
  remote: readonly ModelOption[] = [],
): ModelOption[] {
  if (isRemoteModelKind(kind)) return [...remote];
  return [...getModels(kind)];
}

/** 取当前该用的模型：存的那个已经删了就退回清单第一条 */
export function pickModel(options: readonly ModelOption[], id?: string): ModelOption {
  return options.find((model) => model.id === id) ?? options[0];
}

/** 某个远程种类清单的加载状态与选项 */
export type RemoteModelsSnapshot = {
  /** 加载状态，只有 ready 才能拿清单核对默认模型 */
  status: "idle" | "loading" | "ready" | "error";
  /** 已加载的选项 */
  options: readonly ModelOption[];
};

/**
 * 去掉设置里对不上服务端清单的远程种类默认模型。
 * 设置里存的可能是旧演示清单的 id；清单没加载好或里面找不到时不往新节点上写，
 * 让节点自己落到清单第一条，免得一建出来就是「已下线」。
 * @param defaults 设置里的各种类默认模型（种类 -> 模型 id）
 * @param remote 各远程种类的清单快照
 * @returns 可以放心套用的默认模型；没有要去掉的时返回原对象
 */
export function pruneRemoteDefaults(
  defaults: Record<string, string>,
  remote: Partial<Record<NodeKind, RemoteModelsSnapshot>>,
): Record<string, string> {
  let result = defaults;
  for (const [kind, snapshot] of Object.entries(remote)) {
    const id = defaults[kind];
    if (id === undefined || !snapshot) continue;
    const usable =
      snapshot.status === "ready" && snapshot.options.some((option) => option.id === id);
    if (usable) continue;
    if (result === defaults) result = { ...defaults };
    delete result[kind];
  }
  return result;
}
