"use client";

import * as React from "react";

import { ActivityIcon, AlertTriangleIcon, InfoIcon, RefreshCwIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import type { MonitorSchema, MonitorTask, SystemMonitor } from "@/lib/types";

// 系统监测：回答「进程现在健康吗」，与仪表盘（测出了什么）分工。
// 只读、5s 轮询、页面不可见时暂停——排障页不该成为后台负担。

const REFRESH_MS = 5000;

function fmtUptime(sec: number): string {
  if (sec < 60) return `${sec} 秒`;
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `${d} 天 ${h} 小时`;
  if (h > 0) return `${h} 小时 ${m} 分`;
  return `${m} 分`;
}

function Metric({
  label,
  value,
  hint,
  tone,
}: {
  label: string;
  value: React.ReactNode;
  hint?: string;
  tone?: "warn" | "ok" | "muted";
}) {
  const color =
    tone === "warn" ? "text-amber-600" : tone === "ok" ? "text-emerald-600" : "text-foreground";
  return (
    <div className="space-y-1">
      <div className="text-muted-foreground text-xs">{label}</div>
      <div className={`text-xl font-semibold tabular-nums ${color}`}>{value}</div>
      {hint ? <div className="text-muted-foreground text-[11px] leading-tight">{hint}</div> : null}
    </div>
  );
}

// schemaHint 区分「首次初始化」与「版本升级」。
//
// 不能只看 created > 0：老库升级时 created 也会大于 0（新增几张表），
// 被标成「首次初始化」会让运维误判——以为这是个新部署的库。
// 只有 created 覆盖了现有全部表，才说明这个库是这次建起来的。
function schemaHint(schema?: MonitorSchema): string {
  if (!schema?.tables) return "";
  if (schema.created === 0) return "本次启动无结构变化";
  if (schema.created >= schema.tables) return `本次新建 ${schema.created} 张（首次初始化）`;
  return `新增 ${schema.created} 张（版本升级，共 ${schema.tables} 张）`;
}

// isWildcardListen：监听在通配地址上意味着局域网可达。这个录制代理没有认证，
// 被外部当代理用会往流量库里灌无关请求。
function isWildcardListen(addr?: string): boolean {
  const a = (addr ?? "").trim();
  return a.startsWith("0.0.0.0") || a.startsWith(":") || a.startsWith("[::]");
}

function statusTone(t: MonitorTask): "warn" | "ok" | "muted" {
  if (t.queued || t.paused) return "muted";
  if (t.status === "running" || t.status === "") return t.inflight > 0 ? "ok" : "warn";
  return "muted";
}

export default function SystemMonitorPage() {
  const [data, setData] = React.useState<SystemMonitor | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [auto, setAuto] = React.useState(true);
  const [updatedAt, setUpdatedAt] = React.useState<Date | null>(null);
  const [showAllTasks, setShowAllTasks] = React.useState(false);

  const load = React.useCallback(async (silent = false) => {
    try {
      setData(await api.systemMonitor());
      setUpdatedAt(new Date());
    } catch (e) {
      if (!silent) {
        toast.error(`加载失败：${e instanceof Error ? e.message : String(e)}`);
      }
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  // 轮询：标签页不可见时跳过，回到前台立刻补一次。
  React.useEffect(() => {
    if (!auto) return;
    let timer: ReturnType<typeof setInterval> | undefined;
    const start = () => {
      if (timer) return;
      timer = setInterval(() => {
        if (document.visibilityState === "visible") void load(true);
      }, REFRESH_MS);
    };
    const onVisible = () => {
      if (document.visibilityState === "visible") void load(true);
    };
    start();
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      if (timer) clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [auto, load]);

  const tasks = React.useMemo(() => {
    const all = data?.engine.tasks ?? [];
    if (showAllTasks) return all;
    // 默认只看「有事发生」的任务：在跑、排队、暂停。终态任务在列表页看即可。
    return all.filter((t) => t.inflight > 0 || t.queued || t.paused || !t.status || t.status === "running");
  }, [data, showAllTasks]);

  const warnings = data?.warnings ?? [];
  const warnCount = warnings.filter((w) => w.level === "warn").length;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <ActivityIcon className="text-muted-foreground mt-1 size-5" />
          <div>
            <h1 className="text-lg font-semibold">系统监测</h1>
            <p className="text-muted-foreground text-xs">
              平台运行时快照：进程、连接池、引擎实时与依赖健康。只读，不做任何改动。
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {updatedAt ? (
            <span className="text-muted-foreground text-xs tabular-nums">
              更新于 {updatedAt.toLocaleTimeString()}
            </span>
          ) : null}
          <Button variant={auto ? "default" : "outline"} size="sm" onClick={() => setAuto((v) => !v)}>
            {auto ? "自动刷新 5s" : "已暂停刷新"}
          </Button>
          <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
            <RefreshCwIcon className="size-4" />
          </Button>
        </div>
      </div>

      {warnings.length > 0 ? (
        <div className="space-y-2">
          {warnings.map((w, i) => (
            <div
              key={`${w.title}-${i}`}
              className={`flex items-start gap-3 rounded-md border p-3 ${
                w.level === "warn"
                  ? "border-amber-500/40 bg-amber-500/5"
                  : "border-border bg-muted/30"
              }`}
            >
              {w.level === "warn" ? (
                <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-amber-600" />
              ) : (
                <InfoIcon className="text-muted-foreground mt-0.5 size-4 shrink-0" />
              )}
              <div className="space-y-1">
                <div className="text-sm font-medium">{w.title}</div>
                <div className="text-muted-foreground text-xs leading-relaxed">{w.detail}</div>
              </div>
            </div>
          ))}
        </div>
      ) : (
        <div className="rounded-md border border-emerald-500/30 bg-emerald-500/5 p-3 text-sm">
          未发现需要处置的异常。
        </div>
      )}

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">进程与运行时</CardTitle>
            <CardDescription className="text-xs">
              {data ? `${data.process.go_version} · ${data.process.os}/${data.process.arch}` : "—"}
            </CardDescription>
          </CardHeader>
          <CardContent className="grid grid-cols-2 gap-4">
            <Metric
              label="goroutine"
              value={data?.process.goroutines ?? "—"}
              hint="持续上涨多为泄漏"
              tone={(data?.process.goroutines ?? 0) > 800 ? "warn" : "ok"}
            />
            <Metric label="运行时长" value={data ? fmtUptime(data.process.uptime_seconds) : "—"} />
            <Metric
              label="堆内存"
              value={data ? `${data.process.heap_alloc_mb} MB` : "—"}
              hint={data ? `预留 ${data.process.heap_sys_mb} MB` : undefined}
            />
            <Metric
              label="GC"
              value={data?.process.gc_count ?? "—"}
              hint={data ? `最近暂停 ${data.process.gc_last_pause_ms} ms` : undefined}
            />
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">存储与结构</CardTitle>
            <CardDescription className="text-xs">连接池 + schema 应用情况</CardDescription>
          </CardHeader>
          <CardContent className="grid grid-cols-2 gap-4">
            <Metric
              label="使用中 / 空闲"
              value={data ? `${data.database.in_use} / ${data.database.idle}` : "—"}
              hint={data ? `已打开 ${data.database.open}${data.database.max_open > 0 ? ` / 上限 ${data.database.max_open}` : "（无上限）"}` : undefined}
            />
            <Metric
              label="等待次数"
              value={data?.database.wait_count ?? "—"}
              hint={data ? `累计等待 ${data.database.wait_ms} ms` : undefined}
              tone={(data?.database.wait_count ?? 0) > 0 ? "warn" : "ok"}
            />
            <Metric
              label="数据表"
              value={data?.schema?.tables || "—"}
              hint={schemaHint(data?.schema)}
            />
            <Metric
              label="迁移耗时"
              value={data?.schema ? `${data.schema.elapsed_ms} ms` : "—"}
              hint={data?.schema ? `schema.sql ${data.schema.lines} 行 · 每次启动幂等全量执行` : undefined}
            />
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">引擎实时</CardTitle>
            <CardDescription className="text-xs">
              {data ? `共 ${data.engine.tasks_total} 个任务句柄` : "—"}
            </CardDescription>
          </CardHeader>
          <CardContent className="grid grid-cols-2 gap-4">
            <Metric
              label="运行 / 排队 / 暂停"
              value={data ? `${data.engine.tasks_running} / ${data.engine.tasks_queued} / ${data.engine.tasks_paused}` : "—"}
            />
            <Metric
              label="活跃 LLM 调用"
              value={data?.engine.llm_calls_active ?? "—"}
              hint={data ? `SSE 订阅 ${data.engine.sse_subscribers}` : undefined}
              tone={(data?.engine.llm_calls_active ?? 0) > 0 ? "ok" : "muted"}
            />
            <Metric
              label="activity 丢弃"
              value={data?.engine.activity_drops ?? "—"}
              hint="写库失败累计，丢轨迹"
              tone={(data?.engine.activity_drops ?? 0) > 0 ? "warn" : "ok"}
            />
            <Metric
              label="LLM 就绪"
              value={data ? (data.engine.llm_ready ? "是" : "否") : "—"}
              tone={data?.engine.llm_ready ? "ok" : "warn"}
            />
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">依赖</CardTitle>
            <CardDescription className="text-xs">流量 / 调度</CardDescription>
          </CardHeader>
          <CardContent className="grid grid-cols-2 gap-4">
            <Metric
              label="流量录制"
              value={data ? (data.traffic.enabled ? "已启用" : "未启用") : "—"}
              hint={
                data?.traffic.enabled
                  ? `已录 ${data.traffic.exchanges ?? 0} 条 · 监听 ${data.traffic.listen ?? "—"}`
                  : "取证链会缺口"
              }
              tone={
                !data
                  ? "muted"
                  : !data.traffic.enabled || isWildcardListen(data.traffic.listen)
                    ? "warn"
                    : "ok"
              }
            />
            <Metric
              label="到期计划"
              value={data?.scheduler.due_plans ?? "—"}
              hint="周期任务待触发数"
            />
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <div>
            <CardTitle className="text-sm">任务运行时</CardTitle>
            <CardDescription className="text-xs">
              inflight = 在跑的 planner/worker 数；轮次 = 本进程内已执行的规划轮
            </CardDescription>
          </div>
          <Button variant="outline" size="sm" onClick={() => setShowAllTasks((v) => !v)}>
            {showAllTasks ? "只看活跃" : "显示全部"}
          </Button>
        </CardHeader>
        <CardContent>
          {tasks.length === 0 ? (
            <div className="text-muted-foreground py-6 text-center text-sm">当前没有活跃任务</div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-muted-foreground border-b text-left text-xs">
                    <th className="py-2 pr-3 font-medium">任务</th>
                    <th className="py-2 pr-3 font-medium">状态</th>
                    <th className="py-2 pr-3 text-right font-medium">inflight</th>
                    <th className="py-2 pr-3 text-right font-medium">LLM 调用</th>
                    <th className="py-2 pr-3 text-right font-medium">规划轮</th>
                    <th className="py-2 text-right font-medium">丢弃</th>
                  </tr>
                </thead>
                <tbody>
                  {tasks.map((t) => (
                    <tr key={t.task_id} className="border-b last:border-0">
                      <td className="py-2 pr-3">
                        <span className="font-mono text-xs">#{t.task_id}</span>
                        {t.name ? <span className="ml-2">{t.name}</span> : null}
                      </td>
                      <td className="py-2 pr-3">
                        <Badge variant={statusTone(t) === "warn" ? "destructive" : "secondary"}>
                          {t.queued ? "排队" : t.paused ? "暂停" : t.status || "运行中"}
                        </Badge>
                      </td>
                      <td
                        className={`py-2 pr-3 text-right tabular-nums ${
                          t.status === "running" && t.inflight === 0 ? "text-amber-600" : ""
                        }`}
                      >
                        {t.inflight}
                      </td>
                      <td className="py-2 pr-3 text-right tabular-nums">{t.llm_calls}</td>
                      <td className="py-2 pr-3 text-right tabular-nums">{t.planner_round}</td>
                      <td
                        className={`py-2 text-right tabular-nums ${t.activity_drops > 0 ? "text-amber-600" : ""}`}
                      >
                        {t.activity_drops}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      {warnCount > 0 ? (
        <p className="text-muted-foreground text-xs">
          共 {warnCount} 项需要处置。这里的判定是保守规则（goroutine &gt; 800、连接池等待、activity 丢弃、
          排队且 LLM 未就绪），只在有明确下一步时才会提示。
        </p>
      ) : null}
    </div>
  );
}
