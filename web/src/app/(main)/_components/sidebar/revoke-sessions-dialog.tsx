"use client";

import * as React from "react";

import { toast } from "sonner";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";

// B1 会话吊销的二次确认弹窗：
//   mode="current" —— 只吊销当前这台设备的会话（按 jti 精确失效），其他设备不受影响；
//   mode="all"     —— 吊销全部会话（settings 哨兵时间戳，含历史无 jti token），
//                     所有已登录设备立即失效。同秒边界按 fail-closed 处理：
//                     吊销时点及同秒签发的 token 一律失效，重新登录即恢复。
export function RevokeSessionsDialog({
  mode,
  open,
  onOpenChange,
}: {
  readonly mode: "current" | "all";
  readonly open: boolean;
  readonly onOpenChange: (open: boolean) => void;
}) {
  const [busy, setBusy] = React.useState(false);

  function confirm() {
    setBusy(true);
    const call = mode === "all" ? api.revokeAllSessions() : api.revokeCurrentSession();
    call
      .then(() => {
        onOpenChange(false);
        toast.success(mode === "all" ? "已吊销全部会话" : "已吊销当前会话");
        // 后端已清会话 cookie；内存 token 与跳转照常进行（与退出登录一致）。
        auth.clearToken();
        window.location.href = "/login";
      })
      .catch((err) => toast.error(`吊销失败：${(err as Error).message}`))
      .finally(() => setBusy(false));
  }

  const all = mode === "all";
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{all ? "吊销全部会话？" : "吊销当前会话？"}</AlertDialogTitle>
          <AlertDialogDescription>
            {all
              ? "所有已登录设备（含本机）将立即失效并跳转到登录页；历史旧 token 一并失效。此操作用于怀疑 token 泄露或需要统一强制下线时。"
              : "仅当前这台设备的会话立即失效并跳转到登录页；其他已登录设备不受影响。"}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>取消</AlertDialogCancel>
          <AlertDialogAction
            className={all ? "bg-destructive text-white hover:bg-destructive/90" : ""}
            disabled={busy}
            onClick={(e) => {
              e.preventDefault();
              confirm();
            }}
          >
            {busy ? "吊销中…" : all ? "确认吊销全部" : "确认吊销"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
