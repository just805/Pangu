"use client";

import * as React from "react";

import { CameraIcon, GitCompareIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { api } from "@/lib/api";
import type { AssetDiff, AssetSnapshot } from "@/lib/types";

// 资产快照与变更检测（M-07）。资产图只有「当前状态」，这里回答
// 「这次比上次多了什么、哪个服务换了指纹、哪些资产消失了」。

function fmtTime(v?: string): string {
  if (!v) return "—";
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? v : d.toLocaleString();
}

export function AssetSnapshotDialog({ scopeKey = "" }: { scopeKey?: string }) {
  const [open, setOpen] = React.useState(false);
  const [snaps, setSnaps] = React.useState<AssetSnapshot[]>([]);
  const [diff, setDiff] = React.useState<AssetDiff | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [loading, setLoading] = React.useState(false);

  const load = React.useCallback(async () => {
    try {
      setSnaps(await api.assetSnapshots(scopeKey));
    } catch (e) {
      toast.error(`加载快照失败：${e instanceof Error ? e.message : String(e)}`);
    }
  }, [scopeKey]);

  React.useEffect(() => {
    if (!open) return;
    setLoading(true);
    void load().finally(() => setLoading(false));
  }, [open, load]);

  async function take() {
    setBusy(true);
    try {
      const snap = await api.takeAssetSnapshot(scopeKey);
      toast.success(`已采集快照 #${snap.id}（${snap.asset_count} 项资产）`);
      await load();
      // 采集后立刻展示与上一次的差异——用户点这个按钮的目的就是看变化。
      if (snap.id > 0) {
        const prev = snaps.find((s) => s.id < snap.id);
        const d = await api.assetSnapshotDiff(snap.id, prev?.id);
        setDiff(d);
      }
    } catch (e) {
      toast.error(`采集失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy(false);
    }
  }

  async function showDiff(snap: AssetSnapshot) {
    setBusy(true);
    try {
      setDiff(await api.assetSnapshotDiff(snap.id));
    } catch (e) {
      toast.error(`比较失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          <GitCompareIcon className="size-4" /> 快照变更
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[85vh] w-full overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>资产快照与变更</DialogTitle>
          <DialogDescription>
            采集当前资产状态作为快照；与上一次快照比较，得到新增 / 消失 / 指纹变化。新增资产可直接加入未测清单派单。
          </DialogDescription>
        </DialogHeader>

        <div className="flex items-center justify-between gap-2">
          <span className="text-muted-foreground text-xs">
            {scopeKey ? `范围：${scopeKey}` : "范围：全站资产"}
          </span>
          <Button size="sm" onClick={() => void take()} disabled={busy}>
            <CameraIcon className="size-4" /> 采集快照
          </Button>
        </div>

        <div className="grid gap-1.5">
          <div className="text-sm font-medium">历史快照</div>
          {loading ? (
            <div className="text-muted-foreground py-4 text-center text-sm">加载中…</div>
          ) : snaps.length === 0 ? (
            <div className="text-muted-foreground py-4 text-center text-sm">
              还没有快照。采集两次之后才能看出变化。
            </div>
          ) : (
            <div className="grid gap-1">
              {snaps.map((s) => (
                <div key={s.id} className="flex items-center justify-between gap-2 rounded-md border px-3 py-2">
                  <div className="text-sm">
                    <span className="font-medium">#{s.id}</span>
                    <span className="text-muted-foreground ml-2">{fmtTime(s.created_at)}</span>
                    <Badge variant="secondary" className="ml-2">
                      {s.asset_count} 项
                    </Badge>
                  </div>
                  <Button variant="ghost" size="sm" onClick={() => void showDiff(s)} disabled={busy}>
                    与上次比较
                  </Button>
                </div>
              ))}
            </div>
          )}
        </div>

        {diff ? (
          <div className="grid gap-2 border-t pt-3">
            <div className="text-sm font-medium">
              变更（#{diff.base_id} → #{diff.new_id}）
            </div>
            <div className="text-muted-foreground text-xs">
              {fmtTime(diff.base_at)} → {fmtTime(diff.new_at)}
            </div>
            <div className="flex flex-wrap gap-2">
              <Badge variant="secondary">新增 {diff.added.length}</Badge>
              <Badge variant="secondary">消失 {diff.removed.length}</Badge>
              <Badge variant="secondary">指纹变化 {diff.changed.length}</Badge>
            </div>
            {diff.added.length === 0 && diff.removed.length === 0 && diff.changed.length === 0 ? (
              <div className="text-muted-foreground text-sm">两次快照之间没有变化。</div>
            ) : null}
            {diff.added.length > 0 ? (
              <div className="grid gap-1">
                <div className="text-xs font-medium">新增资产</div>
                {diff.added.slice(0, 50).map((it) => (
                  <div key={`a-${it.asset_id}`} className="text-xs">
                    <Badge variant="outline" className="mr-2">
                      {it.type}
                    </Badge>
                    {it.label}
                  </div>
                ))}
              </div>
            ) : null}
            {diff.changed.length > 0 ? (
              <div className="grid gap-1">
                <div className="text-xs font-medium">指纹变化</div>
                {diff.changed.slice(0, 50).map((it) => (
                  <div key={`c-${it.asset_id}`} className="text-xs">
                    <Badge variant="outline" className="mr-2">
                      {it.type}
                    </Badge>
                    {it.label}
                    <span className="text-muted-foreground ml-2">
                      {it.old_fingerprint.slice(0, 8)} → {it.new_fingerprint.slice(0, 8)}
                    </span>
                  </div>
                ))}
              </div>
            ) : null}
            {diff.removed.length > 0 ? (
              <div className="grid gap-1">
                <div className="text-xs font-medium">消失资产</div>
                {diff.removed.slice(0, 50).map((it) => (
                  <div key={`r-${it.asset_id}`} className="text-xs">
                    <Badge variant="outline" className="mr-2">
                      {it.type}
                    </Badge>
                    {it.label}
                  </div>
                ))}
              </div>
            ) : null}
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
