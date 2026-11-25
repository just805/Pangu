"use client";

import * as React from "react";

// useUnsavedGuard 在存在未保存修改时拦截浏览器刷新/关闭（beforeunload）。
// 只覆盖浏览器级导航——Next App Router 的站内跳转没有原生 blocker，页面内的
// 「切换文件/关闭抽屉」由各页面自己的确认逻辑负责（如 workspace 的关闭按钮）。
// dirty 变 false（保存成功）后监听立即移除，不影响正常导航。
export function useUnsavedGuard(dirty: boolean): void {
  React.useEffect(() => {
    if (!dirty || typeof window === "undefined") return;
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = ""; // Chrome/Firefox 标准触发点（返回非空字符串也可）
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, [dirty]);
}
