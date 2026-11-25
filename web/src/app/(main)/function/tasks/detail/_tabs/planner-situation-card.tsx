"use client";

import * as React from "react";

import { ActivityIcon, ChevronRightIcon, FlaskConicalIcon, RefreshCwIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { usePollInterval } from "@/hooks/use-polling";
import { api } from "@/lib/api";
import type { PlannerSituation } from "@/lib/types";
import { fmtTokens } from "@/lib/format";

// 状态徽标配色（与 sessions-tab statusIcon 的语义一致）。
const stateTone: Record<string, string> = {
  done: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  running: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  paused: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  blocked: "bg-red-500/15 text-red-600 dark:text-red-400",
  exhausted: "bg-zinc-500/15 text-zinc-600 dark:text-zinc-400",
  open: "bg-sky-500/15 text-sky-600 dark:text-sky-400",
  stopped: "bg-zinc-500/15 text-zinc-600 dark:text-zinc-400",
};
const stateLabel: Record<string, string> = {
  done: "完成",
  running: "执行中",
  paused: "已暂停",
  blocked: "受阻",
  exhausted: "耗尽",
  open: "待领",
  stopped: "已停止",
};

function tone(state: string) {
  return stateTone[state] ?? "bg-zinc-500/15 text-zinc-600 dark:text-zinc-400";
}

// PlannerSituationCard 是 planner 态势的结构化卡片（批 2）：意图终态分布、
// 方向产出率 top、主要发现与活跃意图——此前这些以几千字长文本流在会话消息里。
// 30s 轮询刷新（复用 usePollInterval 的页面隐藏暂停）。onJumpCoverage 联动
// 「测试资产覆盖图」页签。
export function PlannerSituationCard({
  taskId,
  onJumpCoverage,
}: {
  taskId: string;
  onJumpCoverage?: () => void;
}) {
  const [data, setData] = React.useState<PlannerSituation | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [refreshing, setRefreshing] = React.useState(false);

  const load = React.useCallback(async () => {
    setRefreshing(true);
    let ok = true;
    try {
      const r = await api.plannerSituation(taskId);
      setData(r);
    } catch {
      // 静默：态势卡是辅助视图，失败不弹错；返回 false 让 usePollInterval
      // 指数退避（后端故障时别把请求打得更密）。
      ok = false;
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
    return ok;
  }, [taskId]);

  usePollInterval(load, 30000, [taskId]);

  const states = data?.states ?? [];
  const totalIntents = states.reduce((sum, s) => sum + s.count, 0);
  const severities = data?.severities ?? [];

  return (
    <Card className="text-xs">
      <CardHeader className="flex-row items-center justify-between py-2.5">
        <CardTitle className="flex items-center gap-1.5 text-xs">
          <ActivityIcon className="size-3.5" /> 规划态势
          <span className="text-muted-foreground font-normal">· {totalIntents} 条意图</span>
        </CardTitle>
        <div className="flex items-center gap-1">
          {onJumpCoverage && (
            <Button size="sm" variant="outline" className="h-6 px-2 text-[11px]" onClick={onJumpCoverage}>
              覆盖图 <ChevronRightIcon />
            </Button>
          )}
          <Button
            size="icon-xs"
            variant="ghost"
            disabled={refreshing}
            onClick={load}
            aria-label="刷新态势"
            title="刷新态势"
          >
            <RefreshCwIcon className={refreshing ? "animate-spin" : undefined} />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="grid gap-3 py-2 sm:grid-cols-2">
        {/* 意图终态分布 */}
        <div className="min-w-0">
          <p className="mb-1.5 font-medium text-muted-foreground">意图终态分布</p>
          {loading ? (
            <p className="text-muted-foreground">加载中…</p>
          ) : states.length ? (
            <div className="flex flex-wrap gap-1">
              {states.map((s) => (
                <span
                  key={s.state}
                  className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 font-medium text-[11px] ${tone(s.state)}`}
                >
                  {stateLabel[s.state] ?? s.state}
                  <span className="tabular-nums">{s.count}</span>
                </span>
              ))}
            </div>
          ) : (
            <p className="text-muted-foreground">暂无意图。</p>
          )}
        </div>

        {/* 主要发现（严重度分布） */}
        <div className="min-w-0">
          <p className="mb-1.5 flex items-center gap-1 font-medium text-muted-foreground">
            <FlaskConicalIcon className="size-3" /> 确认发现
            {data && data.findings > 0 && <span className="tabular-nums">· {data.findings} 条</span>}
          </p>
          {loading ? (
            <p className="text-muted-foreground">加载中…</p>
          ) : severities.length ? (
            <div className="flex flex-wrap gap-1">
              {severities.map((s) => (
                <Badge key={s.severity} variant="outline" className="text-[11px]">
                  {s.severity || "info"} <span className="tabular-nums">{s.count}</span>
                </Badge>
              ))}
            </div>
          ) : (
            <p className="text-muted-foreground">暂无确认发现。</p>
          )}
        </div>

        {/* 方向产出率 top 5：token 消耗最大的终态意图——高消耗低产出的方向别续派。 */}
        <div className="min-w-0 sm:col-span-2">
          <p className="mb-1.5 font-medium text-muted-foreground">方向产出率（token 消耗最大的终态意图 Top 5）</p>
          {loading ? (
            <p className="text-muted-foreground">加载中…</p>
          ) : (data?.directions ?? []).length ? (
            <ul className="space-y-1">
              {(data?.directions ?? []).map((d) => (
                <li key={d.intent_id} className="flex min-w-0 items-center gap-1.5">
                  <span className="shrink-0 rounded bg-muted px-1 font-mono text-[10px] tabular-nums">#{d.intent_id}</span>
                  <span
                    className={`shrink-0 rounded px-1 py-0.5 font-medium text-[10px] ${tone(d.state)}`}
                  >
                    {stateLabel[d.state] ?? d.state}
                  </span>
                  <span className="min-w-0 flex-1 truncate">{d.summary || `意图 #${d.intent_id}`}</span>
                  <span
                    className="shrink-0 font-mono text-[10px] text-muted-foreground tabular-nums"
                    title="该意图累计消耗（输入+输出 token）"
                  >
                    {fmtTokens(d.tokens)}
                  </span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-muted-foreground">暂无终态意图。</p>
          )}
        </div>

        {/* 活跃意图 */}
        {(data?.actives ?? []).length > 0 && (
          <div className="min-w-0 sm:col-span-2">
            <p className="mb-1.5 font-medium text-muted-foreground">正在推进</p>
            <ul className="space-y-1">
              {(data?.actives ?? []).map((a) => (
                <li key={a.intent_id} className="flex min-w-0 items-center gap-1.5">
                  <span className="shrink-0 rounded bg-muted px-1 font-mono text-[10px] tabular-nums">#{a.intent_id}</span>
                  <span className={`shrink-0 rounded px-1 py-0.5 font-medium text-[10px] ${tone(a.state)}`}>
                    {stateLabel[a.state] ?? a.state}
                  </span>
                  <span className="min-w-0 flex-1 truncate">{a.summary || `意图 #${a.intent_id}`}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
