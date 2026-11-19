"use client";

import * as React from "react";

import { EllipsisVertical, KeyRound, LogOut, ShieldOff, MonitorX } from "lucide-react";

import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem, useSidebar } from "@/components/ui/sidebar";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";
import { getInitials } from "@/lib/utils";

import { ChangePasswordDialog } from "./change-password-dialog";
import { RevokeSessionsDialog } from "./revoke-sessions-dialog";

export function NavUser({
  user,
}: {
  readonly user: {
    readonly name: string;
    readonly email: string;
    readonly avatar: string;
  };
}) {
  const { isMobile } = useSidebar();
  const [pwOpen, setPwOpen] = React.useState(false);
  const [revokeMode, setRevokeMode] = React.useState<"current" | "all" | null>(null);

  function handleLogout() {
    // 后端权威清除 HttpOnly 会话 cookie；内存 token 与跳转照常进行——
    // 登出不应被网络故障卡住（api.logout 内部已吞掉后端错误）。
    void api.logout();
    auth.clearToken();
    window.location.href = "/login";
  }

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton
              size="lg"
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
            >
              <Avatar className="h-8 w-8 rounded-lg grayscale">
                <AvatarImage src={user.avatar || undefined} alt={user.name} />
                <AvatarFallback className="rounded-lg">{getInitials(user.name)}</AvatarFallback>
              </Avatar>
              <div className="grid flex-1 text-left text-sm leading-tight">
                <span className="truncate font-medium">{user.name}</span>
                <span className="truncate text-muted-foreground text-xs">{user.email}</span>
              </div>
              <EllipsisVertical className="ml-auto size-4" />
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            className="w-(--radix-dropdown-menu-trigger-width) min-w-56 rounded-lg"
            side={isMobile ? "bottom" : "right"}
            align="end"
            sideOffset={4}
          >
            <DropdownMenuLabel className="p-0 font-normal">
              <div className="flex items-center gap-2 px-1 py-1.5 text-left text-sm">
                <Avatar className="h-8 w-8 rounded-lg">
                  <AvatarImage src={user.avatar || undefined} alt={user.name} />
                  <AvatarFallback className="rounded-lg">{getInitials(user.name)}</AvatarFallback>
                </Avatar>
                <div className="grid flex-1 text-left text-sm leading-tight">
                  <span className="truncate font-medium">{user.name}</span>
                  <span className="truncate text-muted-foreground text-xs">{user.email}</span>
                </div>
              </div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => setPwOpen(true)}>
              <KeyRound />
              修改密码
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => setRevokeMode("current")}>
              <MonitorX />
              吊销当前会话
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onClick={() => setRevokeMode("all")}
              className="text-destructive focus:text-destructive"
            >
              <ShieldOff />
              吊销全部会话
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={handleLogout} className="text-destructive focus:text-destructive">
              <LogOut />
              退出登录
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
      <ChangePasswordDialog open={pwOpen} onOpenChange={setPwOpen} />
      {revokeMode !== null && (
        <RevokeSessionsDialog mode={revokeMode} open={revokeMode !== null} onOpenChange={(o) => !o && setRevokeMode(null)} />
      )}
    </SidebarMenu>
  );
}
