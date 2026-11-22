"use client";

import * as React from "react";

import { RefreshCw, Route } from "lucide-react";

import { AttackChainGraph } from "@/components/attack-chain-graph";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ScrollArea } from "@/components/ui/scroll-area";
import { api } from "@/lib/api";
import type { AttackChain, AttackChainStep } from "@/lib/types";
import { cn } from "@/lib/utils";

const sevCN: Record<string, string> = {
  critical: "严重",
  high: "高危",
  medium: "中危",
  low: "低危",
};

const sevChip: Record<string, string> = {
  critical: "bg-rose-500/15 text-rose-600 dark:text-rose-400",
  high: "bg-orange-500/15 text-orange-600 dark:text-orange-400",
  medium: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  low: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
};

const proofKindCN: Record<string, string> = {
  intent: "意图",
  fact: "事实",
  traffic: "流量",
  evidence: "证据",
};

export function AttackChainTab({ taskId }: { taskId: string }) {
  const [chain, setChain] = React.useState<AttackChain | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<string>("");
  const [selected, setSelected] = React.useState<AttackChainStep | null>(null);

  const load = React.useCallback(() => {
    if (!taskId) return;
    setLoading(true);
    setError("");
    api
      .attackChain(taskId)
      .then((data) => {
        setChain(data);
        setSelected((prev) => {
          if (prev) {
            const next = data.steps.find((s) => s.finding_id === prev.finding_id);
            if (next) return next;
          }
          return data.steps[0] ?? null;
        });
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : "加载攻击链失败"))
      .finally(() => setLoading(false));
  }, [taskId]);

  React.useEffect(() => {
    setChain(null);
    setSelected(null);
    load();
  }, [load]);

  const steps = chain?.steps ?? [];
  const edges = chain?.edges ?? [];

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex items-center gap-2 font-medium text-sm">
          <Route className="size-4 text-muted-foreground" />
          攻击链
        </div>
        <Badge variant="secondary">步骤 {steps.length}</Badge>
        <Badge variant="outline">推进关系 {edges.length}</Badge>
        <div className="ml-auto flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={load} disabled={loading}>
            <RefreshCw className={cn("size-3.5", loading && "animate-spin")} />
            刷新
          </Button>
        </div>
      </div>

      {error ? <p className="text-destructive text-sm">{error}</p> : null}

      {!loading && steps.length === 0 ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-2 py-14 text-center">
            <Route className="size-8 text-muted-foreground/60" />
            <p className="font-medium text-sm">本次任务尚未形成可识别的攻击链</p>
            <p className="max-w-lg text-muted-foreground text-xs">
              攻击链由漏洞的产出意图与探索图血缘自动推导。至少需要两条互相推进的漏洞发现（例如
              凭据泄露 → 登录绕过 → 后台 RCE），或同一意图产出的多条关联漏洞。漏洞未绑定意图、或任务
              尚未产出漏洞时，此处保持为空。
            </p>
          </CardContent>
        </Card>
      ) : null}

      {steps.length > 0 ? (
        <>
          <Card>
            <CardContent className="p-0">
              <AttackChainGraph chain={chain ?? { steps: [], edges: [] }} selectedFindingId={selected?.finding_id} onSelect={setSelected} />
            </CardContent>
          </Card>

          <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_360px]">
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm">链路叙事</CardTitle>
              </CardHeader>
              <CardContent className="p-0">
                <ScrollArea className="h-[420px]">
                  <ol className="flex flex-col gap-0 px-4 pb-4">
                    {steps.map((step) => {
                      const active = selected?.finding_id === step.finding_id;
                      return (
                        <li key={step.finding_id}>
                          <button
                            type="button"
                            onClick={() => setSelected(step)}
                            className={cn(
                              "w-full border-l-2 px-3 py-2.5 text-left transition-colors hover:bg-muted/60",
                              active ? "border-primary bg-muted/70" : "border-border",
                            )}
                          >
                            <div className="flex items-center gap-1.5">
                              <span className="inline-flex size-5 items-center justify-center rounded-full bg-foreground/10 font-semibold text-[11px]">
                                {step.seq}
                              </span>
                              <span
                                className={cn(
                                  "rounded px-1.5 py-0.5 font-medium text-[10px]",
                                  sevChip[step.severity] ?? "bg-muted text-muted-foreground",
                                )}
                              >
                                {sevCN[step.severity] ?? step.severity ?? "未知"}
                              </span>
                              <span className="min-w-0 flex-1 truncate font-medium text-xs">
                                {step.title || step.vuln_class || "未命名发现"}
                              </span>
                            </div>
                            {step.intent_summary ? (
                              <p className="mt-1 line-clamp-2 text-muted-foreground text-xs">{step.intent_summary}</p>
                            ) : null}
                          </button>
                        </li>
                      );
                    })}
                  </ol>
                </ScrollArea>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm">步骤详情</CardTitle>
              </CardHeader>
              <CardContent className="flex flex-col gap-3">
                {selected ? (
                  <>
                    <div>
                      <div className="mb-1 flex items-center gap-1.5">
                        <Badge variant="secondary">步骤 {selected.seq}</Badge>
                        <span
                          className={cn(
                            "rounded px-1.5 py-0.5 font-medium text-[10px]",
                            sevChip[selected.severity] ?? "bg-muted text-muted-foreground",
                          )}
                        >
                          {sevCN[selected.severity] ?? selected.severity ?? "未知"}
                        </span>
                      </div>
                      <p className="font-medium text-sm">{selected.title || selected.vuln_class || "未命名发现"}</p>
                      {selected.vuln_class ? <p className="text-muted-foreground text-xs">{selected.vuln_class}</p> : null}
                    </div>
                    {selected.intent_summary ? (
                      <div>
                        <p className="mb-1 font-medium text-xs">推进方式</p>
                        <p className="text-muted-foreground text-xs">{selected.intent_summary}</p>
                      </div>
                    ) : null}
                    <div>
                      <p className="mb-1 font-medium text-xs">证明点</p>
                      {selected.proofs.length === 0 ? (
                        <p className="text-muted-foreground text-xs">暂无结构化证明点，请查看漏洞详情中的证据。</p>
                      ) : (
                        <ul className="flex flex-col gap-1.5">
                          {selected.proofs.map((p, i) => (
                            <li key={`${p.kind}-${i}`} className="flex gap-1.5 text-xs">
                              <Badge variant="outline" className="h-4 shrink-0 px-1 text-[10px]">
                                {proofKindCN[p.kind] ?? p.kind}
                              </Badge>
                              <span className="min-w-0 break-words text-muted-foreground">{p.label}</span>
                            </li>
                          ))}
                        </ul>
                      )}
                    </div>
                    <p className="text-muted-foreground text-[11px]">
                      发现者 {selected.worker || "-"} · {selected.status || "-"}
                    </p>
                  </>
                ) : (
                  <p className="text-muted-foreground text-sm">在左侧或图中选择一步查看详情。</p>
                )}
              </CardContent>
            </Card>
          </div>
        </>
      ) : null}
    </div>
  );
}
