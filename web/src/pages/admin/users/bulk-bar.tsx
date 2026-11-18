import { Ban, CircleCheck, Loader2 } from "lucide-react";

import type { UserListItem } from "@/api/admin/users/type.d";
import { DataTableBulkActions } from "@/components/admin-ui/data-table-bulk-actions";
import { MotionButton } from "@/components/admin-ui/motion-button";

import type { UserActions } from "./use-user-actions";

/**
 * 勾选后浮在底部的批量条：封禁 / 启用 / 清除。
 * 封禁 / 启用立即执行并带撤销；批量封禁不提供「取消任务」，需要时逐个处理。
 */
function BulkBar({
  users,
  actions,
  onClear,
}: {
  /** 当前勾选的用户 */
  users: UserListItem[];
  actions: UserActions;
  onClear: () => void;
}) {
  const busy = actions.busy === "bulk:status";

  const run = async (to: "active" | "disabled") => {
    if (await actions.bulkStatus(users, to)) onClear();
  };

  return (
    <DataTableBulkActions count={users.length} entityName="用户" onClear={onClear}>
      <MotionButton
        variant="ghost"
        size="xs"
        disabled={busy}
        className="text-destructive hover:text-destructive"
        onClick={() => void run("disabled")}
      >
        {busy ? <Loader2 className="animate-spin" /> : <Ban />}
        封禁
      </MotionButton>
      <MotionButton variant="ghost" size="xs" disabled={busy} onClick={() => void run("active")}>
        <CircleCheck />
        启用
      </MotionButton>
    </DataTableBulkActions>
  );
}

export { BulkBar };
