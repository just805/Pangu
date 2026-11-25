"use client";

import * as React from "react";

import { FileTextIcon, PlusIcon, Trash2Icon } from "lucide-react";
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
} from "@/components/ui/dialog";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import type { ReportTemplate, ReportTemplateInput } from "@/lib/types";

// 报告模板（M-10）：页眉/密级/委托方覆盖 + 章节开关。
// 模板只影响交付件渲染，不改数据；没有默认模板时导出行为与历史一致。

const SECTION_DEFS: { key: string; label: string; hint: string }[] = [
  { key: "exec_summary", label: "执行摘要", hint: "管理层风险结论（LLM 聚合，可人工修订）" },
  { key: "attack_chain", label: "攻击路径", hint: "finding 血缘投影的「初始访问 → 最终影响」叙事" },
  { key: "coverage", label: "覆盖与排除", hint: "资产覆盖矩阵 + 已排除方向与控制点" },
  { key: "reverse", label: "客户端/二进制分析", hint: "静态 triage 与秘密扫描结果" },
  { key: "appendices", label: "附录资产清单", hint: "按类型列出的范围资产" },
];

const emptyForm: ReportTemplateInput = {
  name: "",
  page_header: "",
  classification: "",
  customer_override: "",
  sections: {},
};

export default function ReportTemplatesPage() {
  const [items, setItems] = React.useState<ReportTemplate[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [open, setOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<ReportTemplate | null>(null);
  const [form, setForm] = React.useState<ReportTemplateInput>(emptyForm);
  const [saving, setSaving] = React.useState(false);
  const [pendingDelete, setPendingDelete] = React.useState<ReportTemplate | null>(null);

  const load = React.useCallback(async () => {
    try {
      setItems(await api.reportTemplates());
    } catch (e) {
      toast.error(`加载失败：${e instanceof Error ? e.message : String(e)}`);
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

  function openEdit(tp: ReportTemplate) {
    setEditing(tp);
    setForm({
      name: tp.name,
      page_header: tp.page_header,
      classification: tp.classification,
      customer_override: tp.customer_override,
      sections: { ...(tp.sections ?? {}) },
    });
    setOpen(true);
  }

  function sectionOn(key: string): boolean {
    // 缺省键 = 显示（与后端 chapterEnabled 同口径）。
    const v = form.sections[key];
    return v === undefined ? true : v;
  }

  function toggleSection(key: string, on: boolean) {
    setForm({ ...form, sections: { ...form.sections, [key]: on } });
  }

  async function save() {
    if (!form.name.trim()) return toast.error("请填写模板名称");
    setSaving(true);
    try {
      if (editing) {
        await api.updateReportTemplate(editing.id, form);
        toast.success("已保存");
      } else {
        await api.createReportTemplate(form);
        toast.success("已创建");
      }
      setOpen(false);
      await load();
    } catch (e) {
      toast.error(`保存失败：${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setSaving(false);
    }
  }

  async function setDefault(tp: ReportTemplate) {
    try {
      await api.setDefaultReportTemplate(tp.id);
      toast.success(`「${tp.name}」已设为默认`);
      await load();
    } catch (e) {
      toast.error(`设置失败：${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function doDelete() {
    if (!pendingDelete) return;
    try {
      await api.deleteReportTemplate(pendingDelete.id);
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
          <h1 className="text-2xl font-semibold">报告模板</h1>
          <p className="text-muted-foreground text-sm">
            控制交付件的页眉、密级、委托方与章节开关。没有默认模板时，导出行为与历史完全一致。
          </p>
        </div>
        <Button onClick={openCreate}>
          <PlusIcon className="size-4" />
          新建模板
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
              <FileTextIcon className="size-4" />
              使用内置默认
            </CardTitle>
            <CardDescription>
              当前没有自定义模板，导出沿用内置封面与章节结构。需要给不同甲方出不同版本时再建模板。
            </CardDescription>
          </CardHeader>
        </Card>
      ) : (
        <div className="grid gap-3">
          {items.map((tp) => (
            <Card key={tp.id}>
              <CardHeader className="pb-2">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <CardTitle className="flex flex-wrap items-center gap-2 text-base">
                    {tp.name}
                    {tp.is_default ? <Badge>默认</Badge> : null}
                  </CardTitle>
                  <div className="flex items-center gap-2">
                    {tp.is_default ? null : (
                      <Button variant="outline" size="sm" onClick={() => void setDefault(tp)}>
                        设为默认
                      </Button>
                    )}
                    <Button variant="ghost" size="sm" onClick={() => openEdit(tp)}>
                      编辑
                    </Button>
                    <Button variant="ghost" size="sm" onClick={() => setPendingDelete(tp)}>
                      <Trash2Icon className="text-destructive size-4" />
                    </Button>
                  </div>
                </div>
              </CardHeader>
              <CardContent className="text-muted-foreground grid gap-1 text-xs">
                <div>页眉：{tp.page_header || "（跟随任务名）"}</div>
                <div>密级：{tp.classification || "（内部资料 · 仅限授权方）"}</div>
                <div>委托方：{tp.customer_override || "（按任务关联企业）"}</div>
                <div>
                  章节：
                  {SECTION_DEFS.filter((s) => {
                    const v = tp.sections?.[s.key];
                    return v === undefined ? true : v;
                  })
                    .map((s) => s.label)
                    .join(" / ") || "（全部隐藏）"}
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-h-[85vh] w-full overflow-y-auto sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{editing ? "编辑模板" : "新建模板"}</DialogTitle>
            <DialogDescription>留空的字段沿用内置默认值。</DialogDescription>
          </DialogHeader>
          <div className="grid gap-3">
            <div className="grid gap-1.5">
              <Label htmlFor="t-name">模板名称</Label>
              <Input
                id="t-name"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="如：某银行交付模板"
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="t-header">页眉</Label>
              <Input
                id="t-header"
                value={form.page_header}
                onChange={(e) => setForm({ ...form, page_header: e.target.value })}
                placeholder="留空 = 渗透测试报告 — 任务名"
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="t-class">密级</Label>
              <Input
                id="t-class"
                value={form.classification}
                onChange={(e) => setForm({ ...form, classification: e.target.value })}
                placeholder="留空 = 内部资料 · 仅限授权方"
              />
            </div>
            <div className="grid gap-1.5">
              <Label htmlFor="t-customer">委托方</Label>
              <Input
                id="t-customer"
                value={form.customer_override}
                onChange={(e) => setForm({ ...form, customer_override: e.target.value })}
                placeholder="留空 = 按任务关联企业"
              />
            </div>
            <div className="grid gap-2 border-t pt-3">
              <Label>章节开关</Label>
              {SECTION_DEFS.map((s) => (
                <div key={s.key} className="flex items-center justify-between gap-4">
                  <div className="grid gap-0.5">
                    <span className="text-sm">{s.label}</span>
                    <span className="text-muted-foreground text-xs">{s.hint}</span>
                  </div>
                  <Switch checked={sectionOn(s.key)} onCheckedChange={(v) => toggleSection(s.key, v)} />
                </div>
              ))}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>
              取消
            </Button>
            <Button onClick={() => void save()} disabled={saving}>
              {saving ? "保存中…" : "保存"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={!!pendingDelete} onOpenChange={(v) => !v && setPendingDelete(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除模板？</AlertDialogTitle>
            <AlertDialogDescription>
              删除后导出回到内置默认。「{pendingDelete?.name}」的配置将不可恢复。
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
