"use client";

import * as React from "react";

import { CheckIcon, CopyIcon, FileDownIcon, FileTextIcon, PencilIcon, SparklesIcon } from "lucide-react";
import { Spinner } from "@/components/ui/spinner";
import { toast } from "sonner";

import { Markdown } from "@/components/markdown";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import { copyText } from "@/lib/utils";

export function ReportTab({ taskId }: { taskId: string }) {
  const [report, setReport] = React.useState<string>("");
  const [loading, setLoading] = React.useState(true);
  const [copied, setCopied] = React.useState(false);
  const [exporting, setExporting] = React.useState(false);

  // 执行摘要（A13）：LLM 生成、可人工修订，随报告（预览与 Word）同源导出
  const [summary, setSummary] = React.useState<string>("");
  const [summaryLoading, setSummaryLoading] = React.useState(true);
  const [generating, setGenerating] = React.useState(false);
  const [editing, setEditing] = React.useState(false);
  const [draft, setDraft] = React.useState<string>("");
  const [saving, setSaving] = React.useState(false);

  React.useEffect(() => {
    let active = true;
    setLoading(true);
    api
      .report(taskId)
      .then((text) => {
        if (active) setReport(text);
      })
      .catch(() => {
        if (active) setReport("");
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    api
      .reportSummary(taskId)
      .then((r) => {
        if (active) setSummary(r.summary || "");
      })
      .catch(() => {
        if (active) setSummary("");
      })
      .finally(() => {
        if (active) setSummaryLoading(false);
      });
    return () => {
      active = false;
    };
  }, [taskId]);

  async function copy() {
    if (!report) return;
    const ok = await copyText(report);
    if (ok) {
      setCopied(true);
      toast.success("已复制 Markdown");
      setTimeout(() => setCopied(false), 1500);
    } else {
      toast.error("复制失败，请手动选择文本复制");
    }
  }

  async function generate() {
    setGenerating(true);
    try {
      const r = await api.generateReportSummary(taskId);
      setSummary(r.summary || "");
      setEditing(false);
      toast.success("执行摘要已生成并保存（可编辑后随报告导出）");
    } catch (e) {
      toast.error(`生成失败：${(e as Error).message}`);
    } finally {
      setGenerating(false);
    }
  }

  async function saveSummary() {
    setSaving(true);
    try {
      const r = await api.saveReportSummary(taskId, draft);
      setSummary(r.summary || draft);
      setEditing(false);
      toast.success("执行摘要已保存，预览与 Word 导出同步更新");
    } catch (e) {
      toast.error(`保存失败：${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  let content: React.ReactNode;
  if (loading) {
    content = (
      <div className="flex flex-col items-center justify-center gap-2 rounded-md border border-dashed py-16 text-muted-foreground text-sm">
        <FileTextIcon className="size-8 opacity-40" />
        加载中…
      </div>
    );
  } else if (report) {
    content = (
      <div className="max-h-[60vh] overflow-auto rounded-md border bg-muted/20 p-4">
        <Markdown text={report} />
      </div>
    );
  } else {
    content = (
      <div className="flex flex-col items-center justify-center gap-2 rounded-md border border-dashed py-16 text-muted-foreground text-sm">
        <FileTextIcon className="size-8 opacity-40" />
        暂无报告
      </div>
    );
  }

  let summaryBody: React.ReactNode;
  if (summaryLoading) {
    summaryBody = <p className="text-muted-foreground text-sm">加载中…</p>;
  } else if (editing) {
    summaryBody = (
      <div className="space-y-2">
        <Textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          rows={6}
          className="text-sm"
          placeholder="面向客户管理层的风险结论（≤350 字）"
        />
        <div className="flex gap-2">
          <Button size="sm" disabled={saving} onClick={saveSummary}>
            {saving ? <Spinner data-icon="inline-start" /> : <CheckIcon />} 保存
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={saving}
            onClick={() => {
              setEditing(false);
              setDraft(summary);
            }}
          >
            取消
          </Button>
        </div>
      </div>
    );
  } else if (summary) {
    summaryBody = (
      <p className="text-sm whitespace-pre-wrap leading-relaxed">{summary}</p>
    );
  } else {
    summaryBody = (
      <p className="text-muted-foreground text-sm">
        尚未生成。执行摘要由 LLM 从漏洞发现聚合生成管理层风险结论，可人工修订后随报告导出。
      </p>
    );
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="flex-row items-center justify-between">
          <CardTitle className="flex items-center gap-2 text-sm">
            <SparklesIcon className="size-4" /> 执行摘要
          </CardTitle>
          <div className="flex gap-2">
            {summary && !editing && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setDraft(summary);
                  setEditing(true);
                }}
              >
                <PencilIcon /> 编辑
              </Button>
            )}
            <Button size="sm" variant="outline" disabled={generating || summaryLoading} onClick={generate}>
              {generating ? <Spinner data-icon="inline-start" /> : <SparklesIcon />}{" "}
              {generating ? "生成中…" : summary ? "重新生成" : "生成执行摘要"}
            </Button>
          </div>
        </CardHeader>
        <CardContent>{summaryBody}</CardContent>
      </Card>
      <Card>
        <CardHeader className="flex-row items-center justify-between">
          <CardTitle className="flex items-center gap-2 text-sm">
            <FileTextIcon className="size-4" /> 渗透测试报告（Markdown）
          </CardTitle>
          <div className="flex gap-2">
            {report && (
              <Button size="sm" variant="outline" onClick={copy}>
                {copied ? <CheckIcon /> : <CopyIcon />} 复制
              </Button>
            )}
            {report && (
              <Button
                size="sm"
                variant="outline"
                disabled={exporting}
                onClick={async () => {
                  setExporting(true);
                  try {
                    await api.downloadTaskDocx(taskId);
                    toast.success("Word 已开始下载（含证据附件，文件较大请稍候）");
                  } catch (e) {
                    toast.error(`导出失败：${(e as Error).message}`);
                  } finally {
                    setExporting(false);
                  }
                }}
              >
                {exporting ? <Spinner data-icon="inline-start" /> : <FileDownIcon />} {exporting ? "导出中…" : "导出 Word"}
              </Button>
            )}
          </div>
        </CardHeader>
        <CardContent>{content}</CardContent>
      </Card>
    </div>
  );
}
