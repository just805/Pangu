"use client";

import * as React from "react";

import { KeyRoundIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { api } from "@/lib/api";
import type { APIKey } from "@/lib/types";

// 开放 API 凭证（M-12）：给 CI / 工单 / SOAR 程序化调用平台用。
// 权限等同管理员——页面把这个风险写清楚，避免被当成低权 token 外发。

function fmtTime(v?: string): string {
  if (!v) return "—";
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? v : d.toLocaleString();
}

export default function APIKeysPage() {
  const [items, setItems] = React.useState<APIKey[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [open, setOpen] = React.useState(false);
  const [name, setName] = React.useState("");
  const [expiresDays, setExpiresDays] = React.useState("0");
  const [creating, setCreating] = React.useState(false);
  const [secret, setSecret] = React.useState("");
  const [pendingDelete, setPendingDelete] = React.useState<APIKey | null>(null);

  const load = React.useCallback(async () => {
    try {
      setItems(await api.apiKeys());
    } catch (e) {
      toast.error(`加载失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function create() {
    if (!name.trim()) return toast.error("请填写用途名称");
    setCreating(true);
    try {
      const res = await api.createAPIKey(name.trim(), Number(expiresDays) || 0);
      setSecret(res.secret);
      setName("");
      setExpiresDays("0");
      await load();
    } catch (e) {
      toast.error(`创建失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setCreating(false);
    }
  }

  async function copySecret() {
    try {
      await navigator.clipboard.writeText(secret);
      toast.success("已复制");
    } catch {
      toast.error("复制失败，请手动选中复制");
    }
  }

  async function doDelete() {
    if (!pendingDelete) return;
    try {
      await api.deleteAPIKey(pendingDelete.id);
      toast.success("已删除，该 Key 立即失效");
      setPendingDelete(null);
      await load();
    } catch (e) {
      toast.error(`删除失败：${e instanceof Error ? e.message : String(e)}`);
    }
  }

  return (
    <div className="grid gap-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="grid gap-1">
          <h1 className="text-2xl font-semibold">开放 API</h1>
          <p className="text-muted-foreground text-sm">
            给 CI / 工单 / SOAR 使用的程序化凭证，请求时带 <code className="text-xs">X-API-Key</code> 或
            <code className="text-xs"> Authorization: Bearer pk_…</code>。
            <span className="font-medium text-destructive">权限等同管理员</span>——请勿发到公开渠道。
          </p>
        </div>
        <Dialog
          open={open}
          onOpenChange={(v) => {
            setOpen(v);
            if (!v) setSecret("");
          }}
        >
          <DialogTrigger asChild>
            <Button>
              <PlusIcon className="size-4" />
              新建 Key
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>新建 API Key</DialogTitle>
              <DialogDescription>明文只在创建后显示一次，关闭弹窗后无法再查看。</DialogDescription>
            </DialogHeader>
            {secret ? (
              <div className="grid gap-3">
                <div className="rounded-md border bg-muted/40 p-3">
                  <div className="break-all font-mono text-xs">{secret}</div>
                </div>
                <div className="flex justify-end gap-2">
                  <Button variant="outline" onClick={() => void copySecret()}>
                    复制
                  </Button>
                  <Button onClick={() => setOpen(false)}>我已保存</Button>
                </div>
              </div>
            ) : (
              <div className="grid gap-3">
                <div className="grid gap-1.5">
                  <Label htmlFor="k-name">用途名称</Label>
                  <Input
                    id="k-name"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="如：CI 冒烟 / 工单系统"
                  />
                </div>
                <div className="grid gap-1.5">
                  <Label htmlFor="k-exp">有效期（天，0 = 长期）</Label>
                  <Input
                    id="k-exp"
                    type="number"
                    min={0}
                    className="w-32"
                    value={expiresDays}
                    onChange={(e) => setExpiresDays(e.target.value)}
                  />
                </div>
                <DialogFooter>
                  <Button variant="outline" onClick={() => setOpen(false)}>
                    取消
                  </Button>
                  <Button onClick={() => void create()} disabled={creating}>
                    {creating ? "创建中…" : "创建"}
                  </Button>
                </DialogFooter>
              </div>
            )}
          </DialogContent>
        </Dialog>
      </div>

      {loading ? (
        <Card>
          <CardContent className="text-muted-foreground py-10 text-center text-sm">加载中…</CardContent>
        </Card>
      ) : items.length === 0 ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <KeyRoundIcon className="size-4" />
              还没有 API Key
            </CardTitle>
            <CardDescription>需要程序化创建任务或拉取发现时再建一把，给固定用途单独一把便于审计与吊销。</CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <div className="grid gap-3">
          {items.map((k) => (
            <Card key={k.id}>
              <CardHeader className="pb-2">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <CardTitle className="flex flex-wrap items-center gap-2 text-base">
                    {k.name}
                    <Badge variant="secondary" className="font-mono">
                      {k.key_hint}
                    </Badge>
                  </CardTitle>
                  <Button variant="ghost" size="sm" onClick={() => setPendingDelete(k)}>
                    <Trash2Icon className="text-destructive size-4" />
                  </Button>
                </div>
              </CardHeader>
              <CardContent className="text-muted-foreground grid gap-1 text-xs">
                <div>创建：{fmtTime(k.created_at)}</div>
                <div>最近使用：{fmtTime(k.last_used_at)}</div>
                <div>过期：{k.expires_at ? fmtTime(k.expires_at) : "长期有效"}</div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <AlertDialog open={!!pendingDelete} onOpenChange={(v) => !v && setPendingDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除 API Key？</AlertDialogTitle>
            <AlertDialogDescription>
              「{pendingDelete?.name}」将立即失效，使用它的 CI / 工单流程会收到 401。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={() => void doDelete()}>删除</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
