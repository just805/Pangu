"use client";

import * as React from "react";

import { AlertTriangle, Bug, Coins, Target, TrendingUp } from "lucide-react";
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { type ChartConfig, ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/ui/chart";
import { api } from "@/lib/api";
import type { Insights } from "@/lib/types";
import { cn } from "@/lib/utils";

const trendConfig = {
  critical: { label: "严重", color: "hsl(347 77% 50%)" },
  high: { label: "高危", color: "hsl(24 95% 53%)" },
  medium: { label: "中危", color: "hsl(45 93% 47%)" },
  low: { label: "低危", color: "hsl(160 60% 45%)" },
} satisfies ChartConfig;

function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}

const RANGES = [
  { days: 7, label: "近 7 天" },
  { days: 30, label: "近 30 天" },
  { days: 90, label: "近 90 天" },
];

export default function InsightsPage() {
  const [days, setDays] = React.useState(30);
  const [data, setData] = React.useState<Insights | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");

  const load = React.useCallback((range: number) => {
    setLoading(true);
    setError("");
    api
      .insights(range)
      .then(setData)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : "加载洞察失败"))
      .finally(() => setLoading(false));
  }, []);

  React.useEffect(() => {
    load(days);
  }, [days, load]);

  const totals = data?.totals;
  const trend = React.useMemo(
    () =>
      (data?.trend ?? []).map((d) => ({
        date: d.date.slice(5),
        critical: d.severity.critical,
        high: d.severity.high,
        medium: d.severity.medium,
        low: d.severity.low,
        findings: d.findings,
        tasks: d.tasks,
      })),
    [data?.trend],
  );

  return (
    <div className="flex flex-1 flex-col gap-4 pb-6">
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <h1 className="font-semibold text-lg tracking-tight">洞察</h1>
          <p className="text-muted-foreground text-xs">跨任务风险趋势、严重度分布与单位发现成本（管理层汇报视图）</p>
        </div>
        <div className="flex items-center gap-1">
          {RANGES.map((r) => (
            <Button
              key={r.days}
              size="sm"
              variant={days === r.days ? "default" : "outline"}
              onClick={() => setDays(r.days)}
            >
              {r.label}
            </Button>
          ))}
        </div>
      </div>

      {error ? <p className="text-destructive text-sm">{error}</p> : null}

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
        <Card className="gap-1">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
              <Target className="size-3" /> 任务数
            </div>
            <div className="font-semibold text-2xl tabular-nums">{totals?.tasks ?? 0}</div>
          </CardHeader>
          <CardContent className="text-[10px] text-muted-foreground">统计窗口 {data?.from ?? "-"} ~ {data?.to ?? "-"}</CardContent>
        </Card>
        <Card className="gap-1">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
              <Bug className="size-3" /> 有效漏洞
            </div>
            <div className="font-semibold text-2xl tabular-nums">{totals?.findings ?? 0}</div>
          </CardHeader>
          <CardContent className="flex flex-wrap gap-2 text-[10px]">
            <span className="text-rose-500">严重 {totals?.severity.critical ?? 0}</span>
            <span className="text-orange-500">高危 {totals?.severity.high ?? 0}</span>
            <span className="text-amber-500">中危 {totals?.severity.medium ?? 0}</span>
            <span className="text-emerald-500">低危 {totals?.severity.low ?? 0}</span>
          </CardContent>
        </Card>
        <Card className="gap-1">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
              <AlertTriangle className="size-3" /> 高危占比
            </div>
            <div className="font-semibold text-2xl tabular-nums">
              {totals && totals.findings > 0
                ? `${Math.round(((totals.severity.critical + totals.severity.high) / totals.findings) * 100)}%`
                : "—"}
            </div>
          </CardHeader>
          <CardContent className="text-[10px] text-muted-foreground">
            严重 + 高危 / 有效漏洞
          </CardContent>
        </Card>
        <Card className="gap-1">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
              <Coins className="size-3" /> Token 总消耗
            </div>
            <div className="font-semibold text-2xl tabular-nums">{fmtTokens(totals?.tokens ?? 0)}</div>
          </CardHeader>
          <CardContent className="text-[10px] text-muted-foreground">LLM 输入 + 输出 + 缓存读写</CardContent>
        </Card>
        <Card className="gap-1">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
              <TrendingUp className="size-3" /> 单位发现成本
            </div>
            <div className="font-semibold text-2xl tabular-nums">
              {totals && totals.findings > 0 ? fmtTokens(Math.round(totals.tokens_per_finding)) : "—"}
            </div>
          </CardHeader>
          <CardContent className="text-[10px] text-muted-foreground">token / 每条有效漏洞</CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">风险趋势（按天）</CardTitle>
        </CardHeader>
        <CardContent>
          {loading && !data ? (
            <p className="py-10 text-center text-muted-foreground text-sm">加载中…</p>
          ) : trend.length === 0 ? (
            <p className="py-10 text-center text-muted-foreground text-sm">所选窗口内暂无漏洞数据。</p>
          ) : (
            <ChartContainer config={trendConfig} className="h-[280px] w-full">
              <BarChart data={trend} margin={{ left: 4, right: 8, top: 8, bottom: 0 }}>
                <CartesianGrid vertical={false} strokeDasharray="3 3" />
                <XAxis dataKey="date" tickLine={false} axisLine={false} fontSize={10} />
                <YAxis allowDecimals={false} tickLine={false} axisLine={false} fontSize={10} width={28} />
                <ChartTooltip content={<ChartTooltipContent />} />
                <Bar dataKey="critical" stackId="sev" fill="var(--color-critical)" radius={[0, 0, 0, 0]} />
                <Bar dataKey="high" stackId="sev" fill="var(--color-high)" radius={[0, 0, 0, 0]} />
                <Bar dataKey="medium" stackId="sev" fill="var(--color-medium)" radius={[0, 0, 0, 0]} />
                <Bar dataKey="low" stackId="sev" fill="var(--color-low)" radius={[2, 2, 0, 0]} />
              </BarChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm">按企业聚合</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="border-b text-muted-foreground">
                  <th className="px-4 py-2 text-left font-medium">企业</th>
                  <th className="px-3 py-2 text-right font-medium">任务</th>
                  <th className="px-3 py-2 text-right font-medium">有效漏洞</th>
                  <th className="px-3 py-2 text-right font-medium">严重 / 高危</th>
                  <th className="px-3 py-2 text-right font-medium">Token</th>
                  <th className="px-4 py-2 text-right font-medium">单位发现成本</th>
                </tr>
              </thead>
              <tbody>
                {(data?.companies ?? []).map((c) => (
                  <tr key={c.company_id} className="border-b last:border-0 hover:bg-muted/40">
                    <td className="max-w-[260px] truncate px-4 py-2 font-medium" title={c.name}>
                      {c.name}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums">{c.tasks}</td>
                    <td className="px-3 py-2 text-right tabular-nums">{c.findings}</td>
                    <td className="px-3 py-2 text-right tabular-nums">
                      <span className="text-rose-500">{c.severity.critical}</span>
                      <span className="text-muted-foreground"> / </span>
                      <span className="text-orange-500">{c.severity.high}</span>
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums">{fmtTokens(c.tokens)}</td>
                    <td className="px-4 py-2 text-right tabular-nums">
                      {c.findings > 0 ? fmtTokens(Math.round(c.tokens_per_finding)) : <span className="text-muted-foreground">—</span>}
                    </td>
                  </tr>
                ))}
                {(data?.companies ?? []).length === 0 ? (
                  <tr>
                    <td colSpan={6} className="px-4 py-10 text-center text-muted-foreground">
                      所选窗口内没有企业维度的任务。创建任务时关联企业后，这里会自动聚合。
                    </td>
                  </tr>
                ) : null}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>

      {data ? (
        <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
          <Badge variant="outline">口径</Badge>
          false_positive / duplicate / ignored 不计入有效漏洞；多企业归属任务在各企业下分别计全额成本。
          <span className={cn("ml-auto", loading && "animate-pulse")}>{loading ? "刷新中…" : "数据已加载"}</span>
        </div>
      ) : null}
    </div>
  );
}
