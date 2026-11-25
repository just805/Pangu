"use client";

import * as React from "react";

import { CalendarClockIcon, PlayIcon, PlusIcon, Trash2Icon } from "lucide-react";
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
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { TaskSchedule, TaskScheduleInput } from "@/lib/types";

// 周期任务调度（M-06）。边界：平台只按计划「新建侦察类任务」，
// 不会自动触发复测（复测纯手动是既定策略），页面上明确写出来。

const INTERVAL_PRESETS = [
  { label: "每 1 小时", minutes: 60 },
  { label: "每 6 小时", minutes: 360 },
  { label: "每天", minutes: 1440 },
  { label: "每周", minutes: 10080 },
];

const emptyForm: TaskScheduleInput = {
  name: "",
  description: "",
  goal: "",
  interval_minutes: 1440,
  enabled: true,
  company_ids: [],
  timeout_seconds: 0,
  plan_heartbeat_seconds: 0,
  coverage_enabled: true,
};

function fmtTime(v?: string): string {
  if (!v) return "—";
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return v;
  return d.toLocaleString();
}

function intervalLabel(min: number): string {
  if (min % 10080 === 0) return `每 ${min / 10080} 周`;
  if (min % 1440 === 0) return `每 ${min / 1440} 天`;
  if (min % 60 === 0) return `每 ${min / 60} 小时`;
  return `每 ${min} 分钟`;
}

export default function SchedulesPage() {
  const [items, setItems] = React.useState<TaskSchedule[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [open, setOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<TaskSchedule | null>(null);
  const [form, setForm] = React.useState<TaskScheduleInput>(emptyForm);
  const [saving, setSaving] = React.useState(false);
  const [pendingDelete, setPendingDelete] = React.useState<TaskSchedule | null>(null);

  const load = React.useCallback(async () => {
    try {
      setItems(await api.schedules());
    } catch (e) {
      toast.error(`加载周期任务失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  function openCreate() {
    setEditing(null);
    setForm(emptyForm);
    setOpen(true);
  }

  function openEdit(sc: TaskSchedule) {
    setEditing(sc);
    setForm({
      name: sc.name,
      description: sc.description,
      goal: sc.goal,
      interval_minutes: sc.interval_minutes,
      enabled: sc.enabled,
      company_ids: sc.company_ids ?? [],
      timeout_seconds: sc.timeout_seconds,
      plan_heartbeat_seconds: sc.plan_heartbeat_seconds,
      coverage_enabled: sc.coverage_enabled,
    });
    setOpen(true);
  }

  async function save() {
    if (!form.name.trim()) return toast.error("请填写计划名称");
    if (!form.goal.trim()) return toast.error("请填写任务目标");
    setSaving(true);
    try {
      if (editing) {
        await api.updateSchedule(editing.id, form);
        toast.success("已保存");
      } else {
        await api.createSchedule(form);
        toast.success("已创建（保存后即视为到点，下一轮调度会触发一次）");
      }
      setOpen(false);
      await load();
    } catch (e) {
      toast.error(`保存失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setSaving(false);
    }
  }

  async function toggleEnabled(sc: TaskSchedule, enabled: boolean) {
    try {
      await api.updateSchedule(sc.id, {
        name: sc.name,
        description: sc.description,
        goal: sc.goal,
        interval_minutes: sc.interval_minutes,
        enabled,
        company_ids: sc.company_ids ?? [],
        timeout_seconds: sc.timeout_seconds,
        plan_heartbeat_seconds: sc.plan_heartbeat_seconds,
        coverage_enabled: sc.coverage_enabled,
      });
      setItems((prev) => prev.map((x) => (x.id === sc.id ? { ...x, enabled } : x)));
    } catch (e) {
      toast.error(`切换失败：${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function runNow(sc: TaskSchedule) {
    try {
      await api.runScheduleNow(sc.id);
      toast.success("已触发：已新建一个任务，可在任务列表查看");
      setTimeout(() => void load(), 800);
    } catch (e) {
      toast.error(`触发失败：${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function doDelete() {
    if (!pendingDelete) return;
    try {
      await api.deleteSchedule(pendingDelete.id);
      toast.success("已删除");
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
          <h1 className="text-2xl font-semibold">周期任务</h1>
          <p className="text-muted-foreground text-sm">
            按周期自动新建侦察/攻击面任务。同一计划上一轮未结束时顺延，不叠加执行；
            <span className="font-medium">平台不会自动触发复测</span>——复测仍需在漏洞页面手动发起。
          </p>
        </div>
        <Button onClick={openCreate}>
          <PlusIcon className="size-4" />
          新建计划
        </Button>
      </div>

      {loading ? (
        <Card>
          <CardContent className="text-muted-foreground py-10 text-center text-sm">加载中…</CardContent>
        </Card>
      ) : items.length === 0 ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <CalendarClockIcon className="size-4" />
              还没有周期计划
            </CardTitle>
            <CardDescription>
              适合「每天扫一遍外网资产」「每周复查主站指纹变化」这类重复动作。建一个计划，平台按周期新建任务。
            </CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <div className="grid gap-3">
          {items.map((sc) => (
            <Card key={sc.id}>
              <CardHeader className="pb-2">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div className="grid gap-1">
                    <CardTitle className="flex flex-wrap items-center gap-2 text-base">
                      {sc.name}
                      <Badge variant="secondary">{intervalLabel(sc.interval_minutes)}</Badge>
                      {sc.last_error ? <Badge variant="destructive">上次失败</Badge> : null}
                      {!sc.enabled ? <Badge variant="outline">已停用</Badge> : null}
                    </CardTitle>
                    <p className="text-muted-foreground line-clamp-2 text-sm">{sc.goal}</p>
                  </div>
                  <div className="flex items-center gap-2">
                    <Switch checked={sc.enabled} onCheckedChange={(v) => void toggleEnabled(sc, v)} />
                    <Button variant="outline" size="sm" onClick={() => void runNow(sc)}>
                      <PlayIcon className="size-3.5" />
                      立即运行
                    </Button>
                    <Button variant="ghost" size="sm" onClick={() => openEdit(sc)}>
                      编辑
                    </Button>
                    <Button variant="ghost" size="sm" onClick={() => setPendingDelete(sc)}>
                      <Trash2Icon className="text-destructive size-4" />
                    </Button>
                  </div>
                </div>
              </CardHeader>
              <CardContent className="text-muted-foreground grid gap-1 text-xs">
                <div>下次运行：{fmtTime(sc.next_run_at)}</div>
                <div>上次运行：{fmtTime(sc.last_run_at)}</div>
                {sc.last_task_id ? <div>上次任务 id：{sc.last_task_id}</div> : null}
                {sc.last_error ? <div className="text-destructive">失败原因：{sc.last_error}</div> : null}
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent className="w-full overflow-y-auto sm:max-w-lg">
          <SheetHeader>
            <SheetTitle>{editing ? "编辑计划" : "新建计划"}</SheetTitle>
            <SheetDescription>计划到点后按下方参数新建一个任务，走与手动创建相同的准入与并发限制。</SheetDescription>
          </SheetHeader>
          <div className="grid gap-4 px-4 pb-6">
            <div className="grid gap-1.5">
              <Label htmlFor="s-name">计划名称</Label>
              <Input
                id="s-name"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="如：主站每日巡检"
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="s-desc">任务描述</Label>
              <Input
                id="s-desc"
                value={form.description}
                onChange={(e) => setForm({ ...form, description: e.target.value })}
                placeholder="留空则与计划名称相同"
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="s-goal">任务目标</Label>
              <Textarea
                id="s-goal"
                rows={3}
                value={form.goal}
                onChange={(e) => setForm({ ...form, goal: e.target.value })}
                placeholder="如：对 example.com 做一次完整的外部攻击面测试，重点检查新增子域与暴露接口"
              />
            </div>
            <div className="grid gap-1.5">
              <Label>运行周期</Label>
              <div className="flex flex-wrap gap-2">
                {INTERVAL_PRESETS.map((p) => (
                  <Button
                    key={p.minutes}
                    type="button"
                    size="sm"
                    variant={form.interval_minutes === p.minutes ? "default" : "outline"}
                    onClick={() => setForm({ ...form, interval_minutes: p.minutes })}
                  >
                    {p.label}
                  </Button>
                ))}
              </div>
              <div className="flex items-center gap-2">
                <Input
                  type="number"
                  min={15}
                  className="w-28"
                  value={form.interval_minutes}
                  onChange={(e) => setForm({ ...form, interval_minutes: Number(e.target.value) || 0 })}
                />
                <span className="text-muted-foreground text-xs">分钟（最小 15）</span>
              </div>
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="s-timeout">任务超时（秒，0 = 不限）</Label>
              <Input
                id="s-timeout"
                type="number"
                min={0}
                className="w-40"
                value={form.timeout_seconds}
                onChange={(e) => setForm({ ...form, timeout_seconds: Number(e.target.value) || 0 })}
              />
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">资产覆盖度</Label>
                <p className="text-muted-foreground text-xs">关闭后该计划产生的任务不统计资产覆盖矩阵。</p>
              </div>
              <Switch
                checked={form.coverage_enabled}
                onCheckedChange={(v) => setForm({ ...form, coverage_enabled: v })}
              />
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">启用</Label>
                <p className="text-muted-foreground text-xs">停用后不再自动新建任务，已有任务不受影响。</p>
              </div>
              <Switch checked={form.enabled} onCheckedChange={(v) => setForm({ ...form, enabled: v })} />
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button variant="outline" onClick={() => setOpen(false)}>
                取消
              </Button>
              <Button onClick={() => void save()} disabled={saving}>
                {saving ? "保存中…" : "保存"}
              </Button>
            </div>
          </div>
        </SheetContent>
      </Sheet>

      <AlertDialog open={!!pendingDelete} onOpenChange={(v) => !v && setPendingDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除周期计划？</AlertDialogTitle>
            <AlertDialogDescription>
              「{pendingDelete?.name}」将不再自动新建任务。已创建的任务与其数据不受影响。
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
