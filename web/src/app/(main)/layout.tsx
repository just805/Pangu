"use client";

import type { ReactNode } from "react";
import * as React from "react";

import { AppSidebar } from "@/app/(main)/_components/sidebar/app-sidebar";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { auth } from "@/lib/auth";
import { getClientCookie } from "@/lib/cookie.client";
import {
  SIDEBAR_COLLAPSIBLE_VALUES,
  SIDEBAR_VARIANT_VALUES,
  type SidebarCollapsible,
  type SidebarVariant,
} from "@/lib/preferences/layout";
import { PREFERENCE_DEFAULTS } from "@/lib/preferences/preferences-config";
import { cn } from "@/lib/utils";

import { MainContent } from "./_components/main-content";

// Reads a layout-critical preference from the browser cookie (falls back to the
// default during static-export prerender where document is unavailable). Kept
// fully client-side so the app can be statically exported — no Server Actions /
// next/headers.
function readPref<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  if (typeof document === "undefined") return fallback;
  const value = getClientCookie(key);
  return value && (allowed as readonly string[]).includes(value) ? (value as T) : fallback;
}

export default function Layout({ children }: Readonly<{ children: ReactNode }>) {
  // Client-side auth gate — replaces the Next proxy/middleware that static export
  // disables. 判据是**服务端探活**（/api/version）：HttpOnly 迁移后 token 只存在于
  // cookie，前端无法凭本地状态判断登录态——内存 token 每次刷新后必空，旧「内存
  // 无 token 即弹回 /login」的判据会把已登录用户送走，并与 login 页的「探活 200
  // → 回主界面」形成无限重定向（/login ↔ /function/tasks 循环刷新，回归根因）。
  //   - 200 → 已登录，渲染；
  //   - 401 → 真未登录/会话过期，送 /login（cookie 无效无需清除）；
  //   - 网络故障 → 放行渲染，api 层对真正过期的请求仍有 401 兜底。
  // 探活期间渲染 null，避免受保护 UI（或其 API 调用）闪现。
  const [authed, setAuthed] = React.useState(false);
  React.useEffect(() => {
    let alive = true;
    fetch("/api/version", { credentials: "same-origin" })
      .then((r) => {
        if (!alive) return;
        if (r.ok) {
          setAuthed(true);
        } else {
          window.location.href = "/login";
        }
      })
      .catch(() => {
        if (alive) setAuthed(true);
      });
    return () => {
      alive = false;
    };
  }, []);

  const defaultOpen = typeof document === "undefined" ? true : getClientCookie("sidebar_state") !== "false";
  const variant = readPref<SidebarVariant>(
    "sidebar_variant",
    SIDEBAR_VARIANT_VALUES,
    PREFERENCE_DEFAULTS.sidebar_variant,
  );
  const collapsible = readPref<SidebarCollapsible>(
    "sidebar_collapsible",
    SIDEBAR_COLLAPSIBLE_VALUES,
    PREFERENCE_DEFAULTS.sidebar_collapsible,
  );

  if (!authed) return null;

  return (
    <SidebarProvider
      defaultOpen={defaultOpen}
      style={
        {
          "--sidebar-width": "calc(var(--spacing) * 68)",
        } as React.CSSProperties
      }
    >
      <AppSidebar variant={variant} collapsible={collapsible} />
      <SidebarInset
        className={cn(
          "[html[data-content-layout=centered]_&>*]:mx-auto",
          "[html[data-content-layout=centered]_&>*]:w-full",
          "[html[data-content-layout=centered]_&>*]:max-w-screen-2xl",
          "peer-data-[variant=inset]:border",
          "[--dashboard-header-height:--spacing(12)]",
          "min-w-0 overflow-x-hidden",
        )}
      >
        <MainContent>{children}</MainContent>
      </SidebarInset>
    </SidebarProvider>
  );
}
