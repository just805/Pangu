"use client";

import * as React from "react";

import { ActivityIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { usePollInterval } from "@/hooks/use-polling";
import { api } from "@/lib/api";
import type { InterceptHitStat } from "@/lib/types";

// N12 拦截命中统计：allow/deny/judge 的 Log 与 ask 的 HandleAsk 都落
// intercept_pending——此前规则配了之后「到底命中没有」完全看不见。这里按
// 规则/动作/来源聚合展示，60s 轮询（失败自动退避）。
export function InterceptHitStatsCard() {
  const [items, setItems] = React.useState<InterceptHitStat[]>([]);
  const [total, setTotal] = React.useState(0);

  const load = React.useCallback(async () => {
    try {
      const r = await api.interceptHitStats(30);
      setItems(r.items.slice(0, 8)); // 最多 8 条，按命中数降序
      setTotal(r.items.reduce((sum, x) => sum + x.count, 0));
      return true;
    } catch {
      return false;
    }
  }, []);

  usePollInterval(load, 60000);

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between py-2.5">
        <CardTitle className="flex items-center gap-1.5 text-xs">
          <ActivityIcon className="size-3.5" /> 命中统计（近 30 天）
          <span className="text-muted-foreground font-normal">· {total.toLocaleString()} 次</span>
        </CardTitle>
      </CardHeader>
      <CardContent className="py-2">
        {items.length === 0 ? (
          <p className="text-muted-foreground text-xs">暂无命中记录。</p>
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {items.map((x, i) => (
              <Badge key={`${x.rule_id}-${x.action}-${x.status}-${x.day}-${i}`} variant="outline" className="text-[11px]">
                {x.rule_name}
                <span className="text-muted-foreground">
                  {x.action || x.status} · {x.decision_source} · {x.count}
                </span>
              </Badge>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
