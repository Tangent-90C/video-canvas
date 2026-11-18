import { useCallback, useState } from "react";
import { toast } from "sonner";

import { batchSetStatus, setUserLimit, setUserRole, setUserStatus } from "@/api/admin/users";
import type { UserDetail, UserListItem, UserRole, UserStatus } from "@/api/admin/users/type.d";
import { confirm } from "@/components/admin-ui/confirm-dialog";
import { batchPlan, USER_ROLE_LABEL, type Actor, type BatchUser } from "@/utils/admin/user-rules";

/** 撤销 toast 停留的时间（毫秒） */
const UNDO_MS = 5000;

/** 被操作的用户：行与弹窗都能给出这些字段 */
export type ActionUser = Pick<
  UserListItem,
  | "id"
  | "username"
  | "role"
  | "status"
  | "max_active_tasks"
  | "effective_max_active_tasks"
  | "active_tasks"
>;

/**
 * 用户管理的全部写操作：行菜单、弹窗、批量条共用。
 * 请求失败的全局 toast 由拦截器弹，这里不重复，只在失败后保持界面状态（按钮恢复、浮层不关）。
 * 成功后用接口返回值原地更新（让数字滚动），再静默刷新列表与弹窗。
 * @param options.actor 当前管理员，批量跳过规则要用
 * @param options.patch 同时更新列表行与弹窗详情
 * @param options.refresh 静默重新拉取列表与弹窗详情
 */
export function useUserActions(options: {
  actor: Actor | null;
  patch: (id: number, patch: Partial<UserDetail>) => void;
  refresh: (id?: number) => void;
}) {
  const { actor, patch, refresh } = options;
  const [busy, setBusy] = useState<string | null>(null);

  /** 某个用户的某个动作是否在请求中 */
  const isBusy = useCallback((id: number, kind: string) => busy === `${id}:${kind}`, [busy]);

  /** 包一层 busy：同一时刻只有一个单用户请求，期间相关按钮转圈并禁用 */
  const run = useCallback(
    async <T>(key: string, task: () => Promise<T>): Promise<T | undefined> => {
      setBusy(key);
      try {
        return await task();
      } catch {
        // 全局 toast 已弹，调用方据 undefined 保持浮层打开
        return undefined;
      } finally {
        setBusy(null);
      }
    },
    [],
  );

  /** 设置并发上限（null 为改回全局默认），toast 带撤销 */
  const setLimit = useCallback(
    async (user: ActionUser, value: number | null): Promise<boolean> => {
      const before = { max: user.max_active_tasks, effective: user.effective_max_active_tasks };
      const ok = await run(`${user.id}:limit`, async () => {
        await setUserLimit(user.id, { max_active_tasks: value });
        return true;
      });
      if (!ok) return false;
      patch(user.id, {
        max_active_tasks: value,
        ...(value !== null ? { effective_max_active_tasks: value } : {}),
      });
      refresh(user.id);
      toast.success(
        value === null
          ? `${user.username} 已改回全局默认并发`
          : `${user.username} 的并发上限已设为 ${value}`,
        {
          duration: UNDO_MS,
          action: {
            label: "撤销",
            onClick: () => {
              void setUserLimit(user.id, { max_active_tasks: before.max })
                .then(() => {
                  patch(user.id, {
                    max_active_tasks: before.max,
                    effective_max_active_tasks: before.effective,
                  });
                  refresh(user.id);
                })
                .catch(() => undefined);
            },
          },
        },
      );
      return true;
    },
    [patch, refresh, run],
  );

  /** 封禁 / 启用：可逆，直接执行并给 5 秒撤销，不弹确认框 */
  const setStatus = useCallback(
    async (user: ActionUser, to: UserStatus): Promise<boolean> => {
      const ok = await run(`${user.id}:status`, async () => {
        await setUserStatus(user.id, { status: to });
        return true;
      });
      if (!ok) return false;
      patch(user.id, { status: to });
      refresh(user.id);
      const back: UserStatus = to === "disabled" ? "active" : "disabled";
      toast.success(`已${to === "disabled" ? "封禁" : "启用"} ${user.username}`, {
        duration: UNDO_MS,
        action: {
          label: "撤销",
          onClick: () => {
            void setUserStatus(user.id, { status: back })
              .then(() => {
                patch(user.id, { status: back });
                refresh(user.id);
              })
              .catch(() => undefined);
          },
        },
      });
      return true;
    },
    [patch, refresh, run],
  );

  /** 封禁并取消进行中的任务：任务取消不可逆，所以先弹确认框，成功的 toast 不带撤销 */
  const banAndCancel = useCallback(
    async (user: ActionUser): Promise<boolean> => {
      const done = await confirm({
        title: `封禁 ${user.username} 并取消任务？`,
        destructive: true,
        confirmLabel: "封禁并取消任务",
        description: `将取消 ${user.active_tasks} 个进行中任务，无法撤销。`,
        onConfirm: () => setUserStatus(user.id, { status: "disabled", cancel_active: true }),
      });
      if (!done) return false;
      patch(user.id, { status: "disabled", active_tasks: 0 });
      refresh(user.id);
      toast.success(`已封禁 ${user.username}，取消 ${user.active_tasks} 个任务`);
      return true;
    },
    [patch, refresh],
  );

  /** 修改角色（仅 super_admin，后端会再判断） */
  const changeRole = useCallback(
    async (user: ActionUser, role: UserRole): Promise<boolean> => {
      const ok = await run(`${user.id}:role`, async () => {
        await setUserRole(user.id, role);
        return true;
      });
      if (!ok) return false;
      patch(user.id, { role });
      refresh(user.id);
      toast.success(`${user.username} 已改为「${USER_ROLE_LABEL[role]}」`);
      return true;
    },
    [patch, refresh, run],
  );

  /**
   * 批量封禁 / 启用：无权限的账号先在前端跳过，其余一次提交；toast 带撤销与「已跳过 N 个：原因」。
   * @returns 是否有账号被处理（调用方据此清除勾选）
   */
  const bulkStatus = useCallback(
    async (users: BatchUser[], to: UserStatus): Promise<boolean> => {
      const verb = to === "disabled" ? "封禁" : "启用";
      const plan = batchPlan(actor, users, to === "disabled" ? "ban" : "unban");
      if (plan.run.length === 0) {
        toast.info(
          plan.skipText ? `没有可${verb}的用户。${plan.skipText}` : `选中的用户都已${verb}`,
        );
        return false;
      }
      const result = await run("bulk:status", () =>
        batchSetStatus({ ids: plan.run.map((u) => u.id), status: to }),
      );
      if (!result) return false;
      const okIds = result.results.filter((r) => r.ok).map((r) => r.id);
      const failed = result.results.filter((r) => !r.ok);
      okIds.forEach((id) => patch(id, { status: to }));
      refresh();
      const parts = [
        `已${verb} ${okIds.length} 个用户`,
        plan.skipText,
        failed.length ? `失败 ${failed.length} 个：${failed[0]?.error ?? "未知原因"}` : "",
      ].filter(Boolean);
      const back: UserStatus = to === "disabled" ? "active" : "disabled";
      toast.success(parts.join("，"), {
        duration: UNDO_MS,
        action: okIds.length
          ? {
              label: "撤销",
              onClick: () => {
                void batchSetStatus({ ids: okIds, status: back })
                  .then(() => {
                    okIds.forEach((id) => patch(id, { status: back }));
                    refresh();
                  })
                  .catch(() => undefined);
              },
            }
          : undefined,
      });
      return okIds.length > 0;
    },
    [actor, patch, refresh, run],
  );

  return {
    isBusy,
    busy,
    setLimit,
    setStatus,
    banAndCancel,
    changeRole,
    bulkStatus,
  };
}

/** 用户行 / 弹窗共用的动作集合 */
export type UserActions = ReturnType<typeof useUserActions>;
